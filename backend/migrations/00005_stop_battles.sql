-- +goose Up
ALTER TABLE battle_runs
    DROP CONSTRAINT battle_runs_status_check;

ALTER TABLE battle_runs
    ADD CONSTRAINT battle_runs_status_check
    CHECK (status IN ('queued', 'running', 'completed', 'failed', 'stopped'));

-- +goose Down
UPDATE battle_runs
SET status = 'failed',
    error_code = COALESCE(error_code, 'operator_stopped'),
    error_message = COALESCE(error_message, 'battle was stopped by an operator')
WHERE status = 'stopped';

ALTER TABLE battle_runs
    DROP CONSTRAINT battle_runs_status_check;

ALTER TABLE battle_runs
    ADD CONSTRAINT battle_runs_status_check
    CHECK (status IN ('queued', 'running', 'completed', 'failed'));
