-- Durable owners and serialized admission limits shared by every API replica.
-- +goose Up
ALTER TABLE factions ADD COLUMN owner_id text NOT NULL DEFAULT 'legacy-admin';
ALTER TABLE battle_runs ADD COLUMN owner_id text NOT NULL DEFAULT 'legacy-admin';
ALTER TABLE battle_runs ADD COLUMN ai_executions integer NOT NULL DEFAULT 0;
ALTER TABLE faction_background_generations ADD COLUMN runtime text NOT NULL DEFAULT 'cli' CHECK(runtime IN ('cli','api'));
CREATE TABLE agent_execution_leases (id uuid PRIMARY KEY,expires_at timestamptz NOT NULL);
CREATE INDEX agent_execution_expiry ON agent_execution_leases(expires_at);
CREATE INDEX battle_runs_owner_created ON battle_runs(owner_id,created_at);
CREATE TABLE runtime_policy (
 id boolean PRIMARY KEY DEFAULT true CHECK(id),
 ai_enabled boolean NOT NULL DEFAULT true,
 daily_battles integer NOT NULL DEFAULT 20 CHECK(daily_battles>0),
 user_unfinished integer NOT NULL DEFAULT 2 CHECK(user_unfinished>0),
 global_unfinished integer NOT NULL DEFAULT 4 CHECK(global_unfinished>0),
 decision_executions integer NOT NULL DEFAULT 100 CHECK(decision_executions>0)
);
INSERT INTO runtime_policy(id) VALUES(true);
-- +goose StatementBegin
CREATE FUNCTION admit_friend_battle() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE p runtime_policy%ROWTYPE;
BEGIN
 -- Existing IDs reach the unique constraint without spending another allowance.
 IF NEW.owner_id='legacy-admin' THEN RETURN NEW; END IF;
 IF EXISTS(SELECT 1 FROM battle_runs WHERE id=NEW.id) THEN RETURN NEW; END IF;
 SELECT * INTO p FROM runtime_policy WHERE id FOR UPDATE;
 IF EXISTS(SELECT 1 FROM battle_runs WHERE id=NEW.id) THEN RETURN NEW; END IF;
 IF (SELECT count(*) FROM battle_runs WHERE owner_id=NEW.owner_id AND created_at >= date_trunc('day',now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC') >= p.daily_battles
 OR (SELECT count(*) FROM battle_runs WHERE owner_id=NEW.owner_id AND status IN ('queued','running')) >= p.user_unfinished
 OR (SELECT count(*) FROM battle_runs WHERE status IN ('queued','running')) >= p.global_unfinished THEN
  RAISE EXCEPTION 'battle allowance exhausted' USING ERRCODE='P0429';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER friend_battle_admission BEFORE INSERT ON battle_runs FOR EACH ROW EXECUTE FUNCTION admit_friend_battle();

-- Claims for CLI decisions and background jobs serialize on the same policy row.
-- +goose StatementBegin
CREATE FUNCTION admit_agent_execution() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE p runtime_policy%ROWTYPE; is_cli boolean; count_used integer;
BEGIN
 IF NEW.status <> 'running' THEN RETURN NEW; END IF;
 SELECT * INTO p FROM runtime_policy WHERE id FOR UPDATE;
 IF NOT p.ai_enabled THEN RETURN NULL; END IF;
 DELETE FROM agent_execution_leases WHERE expires_at<=now();
 is_cli := NEW.runtime='cli';
 IF is_cli AND EXISTS(SELECT 1 FROM agent_execution_leases WHERE expires_at>now() AND id<>NEW.id) THEN RETURN NULL; END IF;
 IF TG_TABLE_NAME='battle_decisions' THEN
  UPDATE battle_runs SET ai_executions=ai_executions+1 WHERE id=NEW.battle_id AND ai_executions<p.decision_executions RETURNING ai_executions INTO count_used;
  IF count_used IS NULL THEN
   NEW.status:='failed'; NEW.error_category:='decision_budget'; NEW.resolved_at:=now(); NEW.lease_owner:=NULL; NEW.lease_expires_at:=NULL;
  END IF;
 END IF;
 IF NEW.status='running' AND is_cli THEN
  INSERT INTO agent_execution_leases(id,expires_at) VALUES(NEW.id,CASE WHEN TG_TABLE_NAME='battle_decisions' THEN (to_jsonb(NEW)->>'lease_expires_at')::timestamptz ELSE (to_jsonb(NEW)->>'expires_at')::timestamptz END + interval '5 seconds') ON CONFLICT(id) DO UPDATE SET expires_at=EXCLUDED.expires_at;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER agent_decision_admission BEFORE UPDATE OF lease_owner ON battle_decisions FOR EACH ROW EXECUTE FUNCTION admit_agent_execution();
CREATE TRIGGER agent_generation_admission BEFORE UPDATE OF worker_id ON faction_background_generations FOR EACH ROW EXECUTE FUNCTION admit_agent_execution();
-- +goose StatementBegin
CREATE FUNCTION release_agent_execution() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 DELETE FROM agent_execution_leases WHERE id=NEW.id;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER release_decision_execution AFTER UPDATE OF usage ON battle_decisions FOR EACH ROW EXECUTE FUNCTION release_agent_execution();
CREATE TRIGGER release_generation_execution AFTER UPDATE OF usage ON faction_background_generations FOR EACH ROW EXECUTE FUNCTION release_agent_execution();
GRANT SELECT,UPDATE(id) ON runtime_policy TO battlefield_mcp;
GRANT SELECT(id,ai_executions) ON battle_runs TO battlefield_mcp;
GRANT SELECT,INSERT,UPDATE,DELETE ON agent_execution_leases TO battlefield_mcp;
GRANT UPDATE(ai_executions) ON battle_runs TO battlefield_mcp;

-- New model windows immediately fall back when AI is stopped or their budget is spent.
-- +goose StatementBegin
CREATE FUNCTION gate_new_ai_job() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE enabled boolean; budget integer;
BEGIN
 SELECT ai_enabled,decision_executions INTO enabled,budget FROM runtime_policy WHERE id;
 IF TG_TABLE_NAME='battle_decisions' THEN
  IF NEW.controller_type='model' AND NEW.status='pending' AND (NOT enabled OR (SELECT ai_executions FROM battle_runs WHERE id=NEW.battle_id)>=budget) THEN
   NEW.status:='failed'; NEW.error_category:='ai_disabled_or_budget'; NEW.resolved_at:=now();
  END IF;
 ELSIF NOT enabled THEN RAISE EXCEPTION 'AI temporarily disabled' USING ERRCODE='P0429'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER gate_decision BEFORE INSERT ON battle_decisions FOR EACH ROW EXECUTE FUNCTION gate_new_ai_job();
CREATE TRIGGER gate_generation BEFORE INSERT ON faction_background_generations FOR EACH ROW EXECUTE FUNCTION gate_new_ai_job();
CREATE VIEW friend_ai_usage AS
 SELECT r.owner_id AS identity,d.profile_id,d.created_at,d.usage FROM battle_decisions d JOIN battle_runs r ON r.id=d.battle_id
 UNION ALL SELECT actor_id,profile_id,created_at,usage FROM faction_background_generations;

-- +goose Down
DROP VIEW friend_ai_usage;
DROP TRIGGER gate_generation ON faction_background_generations;
DROP TRIGGER gate_decision ON battle_decisions;
DROP FUNCTION gate_new_ai_job;
DROP TRIGGER release_generation_execution ON faction_background_generations;
DROP TRIGGER release_decision_execution ON battle_decisions;
DROP FUNCTION release_agent_execution();
DROP TABLE agent_execution_leases;
ALTER TABLE faction_background_generations DROP COLUMN runtime;
DROP TRIGGER agent_generation_admission ON faction_background_generations;
DROP TRIGGER agent_decision_admission ON battle_decisions;
DROP FUNCTION admit_agent_execution();
DROP TRIGGER friend_battle_admission ON battle_runs;
DROP FUNCTION admit_friend_battle();
DROP TABLE runtime_policy;
DROP INDEX battle_runs_owner_created;
ALTER TABLE battle_runs DROP COLUMN ai_executions, DROP COLUMN owner_id;
ALTER TABLE factions DROP COLUMN owner_id;
