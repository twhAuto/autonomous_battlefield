-- Plays every league match as one battle: groups become dual tournaments and knockout
-- ties lose their second leg. Leagues in the earlier format are deleted, and with them
-- their entrants and fixtures; their battles remain, outside any league. Both directions
-- wait until no league runs and no league battle is queued or running, since a cancelled
-- league's fixtures are how the scheduler retries stopping its battles.
-- +goose Up
-- +goose StatementBegin
DO $$
BEGIN
 IF EXISTS (SELECT 1 FROM leagues WHERE status = 'running')
    OR EXISTS (SELECT 1 FROM league_fixtures f JOIN battle_runs b ON b.id = f.battle_id WHERE b.status IN ('queued', 'running')) THEN
  RAISE EXCEPTION 'cancel running leagues and wait for their battles to stop before changing the league format';
 END IF;
END $$;
-- +goose StatementEnd
DELETE FROM leagues;
-- Group fixtures are rounds 1 to 3 of a dual tournament, ties 1 and 2; knockout
-- fixtures are round 1 of their stage.
ALTER TABLE league_fixtures DROP COLUMN leg,
    ADD CONSTRAINT league_fixtures_match_check
        CHECK (tie >= 1 AND CASE WHEN stage = 'group' THEN round <= 3 AND tie <= 2 ELSE round = 1 END);

-- +goose Down
-- The earlier release cannot read dual-tournament leagues, so they are deleted too.
-- +goose StatementBegin
DO $$
BEGIN
 IF EXISTS (SELECT 1 FROM leagues WHERE status = 'running')
    OR EXISTS (SELECT 1 FROM league_fixtures f JOIN battle_runs b ON b.id = f.battle_id WHERE b.status IN ('queued', 'running')) THEN
  RAISE EXCEPTION 'cancel running leagues and wait for their battles to stop before changing the league format';
 END IF;
END $$;
-- +goose StatementEnd
DELETE FROM leagues;
ALTER TABLE league_fixtures DROP CONSTRAINT league_fixtures_match_check,
    ADD COLUMN leg integer NOT NULL CHECK (leg IN (1, 2));
