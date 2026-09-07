-- Adds the pause flag and an index that supports claiming only unpaused queued or running
-- battles.

-- +goose Up
ALTER TABLE battle_runs
    ADD COLUMN paused boolean NOT NULL DEFAULT false;

CREATE INDEX battle_runs_unpaused_claim_idx
ON battle_runs (status, lease_expires_at, created_at, id)
WHERE status IN ('queued', 'running') AND NOT paused;

-- +goose Down
DROP INDEX battle_runs_unpaused_claim_idx;
ALTER TABLE battle_runs DROP COLUMN paused;
