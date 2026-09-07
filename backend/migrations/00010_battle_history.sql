-- +goose Up
CREATE INDEX battle_runs_history_idx ON battle_runs (created_at DESC, id DESC);

-- +goose Down
DROP INDEX battle_runs_history_idx;
