-- Exempts Admin-created battles from the per-person daily and unfinished allowances
-- and the global cap. The API sets admission_exempt only for an authenticated Admin;
-- Players, Developers and CI keep every limit. Exempt battles still count as
-- unfinished for everyone else, because they occupy the same engine.

-- +goose Up
ALTER TABLE battle_runs ADD COLUMN admission_exempt boolean NOT NULL DEFAULT false;
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

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION admit_friend_battle() RETURNS trigger LANGUAGE plpgsql AS $$
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
ALTER TABLE battle_runs DROP COLUMN admission_exempt;
