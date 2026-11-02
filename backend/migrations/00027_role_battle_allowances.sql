-- Gives Players and Testers separate daily and unfinished battle allowances. The API
-- sets tester_allowance only for an authenticated Tester; every other battle that is
-- not admission_exempt (Admin) uses the Player allowances. The global cap is shared.

-- +goose Up
ALTER TABLE runtime_policy RENAME COLUMN daily_battles TO player_daily_battles;
ALTER TABLE runtime_policy RENAME COLUMN user_unfinished TO player_unfinished;
ALTER TABLE runtime_policy
 ALTER COLUMN player_daily_battles SET DEFAULT 5,
 ALTER COLUMN player_unfinished SET DEFAULT 1,
 ADD COLUMN tester_daily_battles integer NOT NULL DEFAULT 10 CHECK(tester_daily_battles>0),
 ADD COLUMN tester_unfinished integer NOT NULL DEFAULT 2 CHECK(tester_unfinished>0);
-- Only allowances still at the previous defaults move; operator-set values stay.
UPDATE runtime_policy SET
 player_daily_battles = CASE WHEN player_daily_battles=20 THEN 5 ELSE player_daily_battles END,
 player_unfinished = CASE WHEN player_unfinished=2 THEN 1 ELSE player_unfinished END;
ALTER TABLE battle_runs ADD COLUMN tester_allowance boolean NOT NULL DEFAULT false;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION admit_friend_battle() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE p runtime_policy%ROWTYPE; daily integer; unfinished integer;
BEGIN
 -- Existing IDs reach the unique constraint without spending another allowance.
 IF NEW.owner_id='legacy-admin' OR NEW.admission_exempt THEN RETURN NEW; END IF;
 IF EXISTS(SELECT 1 FROM battle_runs WHERE id=NEW.id) THEN RETURN NEW; END IF;
 SELECT * INTO p FROM runtime_policy WHERE id FOR UPDATE;
 IF EXISTS(SELECT 1 FROM battle_runs WHERE id=NEW.id) THEN RETURN NEW; END IF;
 daily := CASE WHEN NEW.tester_allowance THEN p.tester_daily_battles ELSE p.player_daily_battles END;
 unfinished := CASE WHEN NEW.tester_allowance THEN p.tester_unfinished ELSE p.player_unfinished END;
 IF (SELECT count(*) FROM battle_runs WHERE owner_id=NEW.owner_id AND created_at >= date_trunc('day',now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC') >= daily
 OR (SELECT count(*) FROM battle_runs WHERE owner_id=NEW.owner_id AND status IN ('queued','running')) >= unfinished
 OR (SELECT count(*) FROM battle_runs WHERE status IN ('queued','running')) >= p.global_unfinished THEN
  RAISE EXCEPTION 'battle allowance exhausted' USING ERRCODE='P0429';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION admit_friend_battle() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE p runtime_policy%ROWTYPE;
BEGIN
 -- Existing IDs reach the unique constraint without spending another allowance.
 IF NEW.owner_id='legacy-admin' OR NEW.admission_exempt THEN RETURN NEW; END IF;
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
ALTER TABLE battle_runs DROP COLUMN tester_allowance;
ALTER TABLE runtime_policy DROP COLUMN tester_daily_battles, DROP COLUMN tester_unfinished;
ALTER TABLE runtime_policy RENAME COLUMN player_daily_battles TO daily_battles;
ALTER TABLE runtime_policy RENAME COLUMN player_unfinished TO user_unfinished;
-- The current Player allowances stay: an earlier default and an operator's value
-- cannot be told apart, and keeping either never loosens a limit.
ALTER TABLE runtime_policy ALTER COLUMN daily_battles SET DEFAULT 20, ALTER COLUMN user_unfinished SET DEFAULT 2;
