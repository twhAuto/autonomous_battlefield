-- Shared CLI capacity across battle decisions and background generation.
-- +goose Up
ALTER TABLE runtime_policy ADD COLUMN cli_concurrency integer NOT NULL DEFAULT 4 CHECK(cli_concurrency BETWEEN 1 AND 4);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION admit_agent_execution() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE p runtime_policy%ROWTYPE; is_cli boolean; count_used integer;
BEGIN
 IF NEW.status <> 'running' THEN RETURN NEW; END IF;
 SELECT * INTO p FROM runtime_policy WHERE id FOR UPDATE;
 IF NOT p.ai_enabled THEN RETURN NULL; END IF;
 DELETE FROM agent_execution_leases WHERE expires_at<=now();
 is_cli := NEW.runtime='cli';
 IF is_cli AND (SELECT count(*) FROM agent_execution_leases WHERE expires_at>now() AND id<>NEW.id) >= p.cli_concurrency THEN RETURN NULL; END IF;
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

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION admit_agent_execution() RETURNS trigger LANGUAGE plpgsql AS $$
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
ALTER TABLE runtime_policy DROP COLUMN cli_concurrency;
