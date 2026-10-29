-- Lets admins disqualify league entrants. An entrant records when, by whom and why it
-- was disqualified; a forfeited fixture records which side was: home, away or both.
-- Earlier releases ignore the new columns and never write a forfeit.
-- +goose Up
ALTER TABLE league_entrants
    ADD COLUMN disqualified_at timestamptz,
    ADD COLUMN disqualified_by text,
    ADD COLUMN disqualification_reason text NOT NULL DEFAULT '' CHECK (char_length(disqualification_reason) <= 200),
    ADD CONSTRAINT league_entrants_disqualification_check
        CHECK ((disqualified_at IS NULL) = (disqualified_by IS NULL) AND (disqualified_at IS NOT NULL OR disqualification_reason = ''));
ALTER TABLE league_fixtures
    ADD COLUMN forfeit text CHECK (forfeit IN ('home', 'away', 'both')),
    DROP CONSTRAINT league_fixtures_status_check,
    ADD CONSTRAINT league_fixtures_status_check CHECK (status IN ('pending', 'running', 'completed', 'void', 'cancelled', 'forfeited')),
    ADD CONSTRAINT league_fixtures_forfeit_status_check CHECK ((status = 'forfeited') = (forfeit IS NOT NULL));

-- +goose Down
-- Earlier releases cannot read forfeits, so leagues with a disqualified entrant are
-- deleted; their battles remain, outside any league. Like the earlier format change, it
-- waits until those leagues have stopped and none of their battles is queued or running.
-- +goose StatementBegin
DO $$
BEGIN
 IF EXISTS (
  SELECT 1 FROM leagues l
  WHERE EXISTS (SELECT 1 FROM league_entrants e WHERE e.league_id = l.id AND e.disqualified_at IS NOT NULL)
    AND (l.status = 'running'
      OR EXISTS (SELECT 1 FROM league_fixtures f JOIN battle_runs b ON b.id = f.battle_id WHERE f.league_id = l.id AND b.status IN ('queued', 'running')))) THEN
  RAISE EXCEPTION 'cancel leagues with a disqualified entrant and wait for their battles to stop before removing disqualification';
 END IF;
END $$;
-- +goose StatementEnd
DELETE FROM leagues WHERE id IN (SELECT league_id FROM league_entrants WHERE disqualified_at IS NOT NULL);
ALTER TABLE league_fixtures
    DROP CONSTRAINT league_fixtures_forfeit_status_check,
    DROP CONSTRAINT league_fixtures_status_check,
    DROP COLUMN forfeit,
    ADD CONSTRAINT league_fixtures_status_check CHECK (status IN ('pending', 'running', 'completed', 'void', 'cancelled'));
ALTER TABLE league_entrants
    DROP CONSTRAINT league_entrants_disqualification_check,
    DROP COLUMN disqualification_reason,
    DROP COLUMN disqualified_by,
    DROP COLUMN disqualified_at;
