-- Adds AI leagues: entrants in groups of four, their group and knockout fixtures, and
-- a per-battle exemption from the model decision budget that only league battles set.
-- Exempt battles still count every execution in ai_executions.

-- +goose Up
CREATE TABLE leagues (
    id uuid PRIMARY KEY,
    owner_id text NOT NULL,
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    status text NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'completed', 'cancelled')),
    request_digest text NOT NULL,
    scenario_ids text[] NOT NULL CHECK (cardinality(scenario_ids) BETWEEN 1 AND 5),
    requested_ticks integer NOT NULL CHECK (requested_ticks BETWEEN 1 AND 10000),
    tick_duration text,
    decision_interval_ticks integer CHECK (decision_interval_ticks BETWEEN 1 AND 100),
    group_count integer NOT NULL CHECK (group_count IN (2, 4)),
    champion_entrant integer CHECK (champion_entrant BETWEEN 0 AND 15),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    CHECK ((status = 'running') = (completed_at IS NULL)),
    CHECK (champion_entrant IS NULL OR status = 'completed')
);
CREATE INDEX leagues_created_at ON leagues (created_at DESC, id DESC);

-- Faction names are copied at creation, so deleting a faction never breaks a league.
CREATE TABLE league_entrants (
    league_id uuid NOT NULL REFERENCES leagues (id) ON DELETE CASCADE,
    entrant_index integer NOT NULL CHECK (entrant_index BETWEEN 0 AND 15),
    group_index integer NOT NULL CHECK (group_index BETWEEN 0 AND 3),
    faction_id uuid NOT NULL,
    faction_name text NOT NULL,
    controller jsonb NOT NULL,
    PRIMARY KEY (league_id, entrant_index),
    UNIQUE (league_id, faction_id)
);

CREATE TABLE league_fixtures (
    id uuid PRIMARY KEY,
    league_id uuid NOT NULL REFERENCES leagues (id) ON DELETE CASCADE,
    sequence integer NOT NULL CHECK (sequence >= 0),
    stage text NOT NULL CHECK (stage IN ('group', 'quarterfinal', 'semifinal', 'final')),
    group_index integer CHECK (group_index BETWEEN 0 AND 3),
    round integer NOT NULL CHECK (round >= 1),
    tie integer NOT NULL DEFAULT 0 CHECK (tie >= 0),
    leg integer NOT NULL CHECK (leg IN (1, 2)),
    home_entrant integer NOT NULL,
    away_entrant integer NOT NULL,
    scenario_id text NOT NULL,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'completed', 'void', 'cancelled')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 2),
    battle_id uuid UNIQUE REFERENCES battle_runs (id) ON DELETE SET NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (league_id, sequence),
    FOREIGN KEY (league_id, home_entrant) REFERENCES league_entrants (league_id, entrant_index),
    FOREIGN KEY (league_id, away_entrant) REFERENCES league_entrants (league_id, entrant_index),
    CHECK (home_entrant <> away_entrant),
    CHECK ((stage = 'group') = (group_index IS NOT NULL)),
    CHECK (status <> 'running' OR battle_id IS NOT NULL)
);

ALTER TABLE battle_runs ADD COLUMN decision_budget_exempt boolean NOT NULL DEFAULT false;
-- Claims run admit_agent_execution as the AI gateway login, which must read the flag.
GRANT SELECT(decision_budget_exempt) ON battle_runs TO battlefield_ai_gateway;

-- New model windows immediately fall back when AI is stopped or a non-exempt budget is spent.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION gate_new_ai_job() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE enabled boolean; budget integer;
BEGIN
 SELECT ai_enabled,decision_executions INTO enabled,budget FROM runtime_policy WHERE id;
 IF TG_TABLE_NAME='battle_decisions' THEN
  IF NEW.controller_type='model' AND NEW.status='pending' AND (NOT enabled OR (SELECT ai_executions>=budget AND NOT decision_budget_exempt FROM battle_runs WHERE id=NEW.battle_id)) THEN
   NEW.status:='failed'; NEW.error_category:='ai_disabled_or_budget'; NEW.resolved_at:=now();
  END IF;
 ELSIF NOT enabled THEN RAISE EXCEPTION 'AI temporarily disabled' USING ERRCODE='P0429'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

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
  UPDATE battle_runs SET ai_executions=ai_executions+1 WHERE id=NEW.battle_id AND (ai_executions<p.decision_executions OR decision_budget_exempt) RETURNING ai_executions INTO count_used;
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
-- Refuses to strand running league battles; completed and cancelled leagues are dropped.
-- +goose StatementBegin
DO $$
BEGIN
 IF EXISTS (SELECT 1 FROM leagues WHERE status = 'running') THEN
  RAISE EXCEPTION 'cancel running leagues before removing them';
 END IF;
END $$;
-- +goose StatementEnd

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

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION gate_new_ai_job() RETURNS trigger LANGUAGE plpgsql AS $$
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

REVOKE SELECT(decision_budget_exempt) ON battle_runs FROM battlefield_ai_gateway;
ALTER TABLE battle_runs DROP COLUMN decision_budget_exempt;
DROP TABLE league_fixtures;
DROP TABLE league_entrants;
DROP TABLE leagues;
