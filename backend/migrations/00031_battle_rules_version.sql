-- Stores each battle's frozen rules version as a column, so battle history can filter by
-- it, and list the versions in use, without reading every scenario snapshot.

-- +goose Up
ALTER TABLE battle_runs ADD COLUMN rules_version text GENERATED ALWAYS AS (scenario_snapshot->>'rulesVersion') STORED;
CREATE INDEX battle_runs_rules_version_history_idx ON battle_runs (rules_version, created_at DESC, id DESC);

-- +goose Down
DROP INDEX battle_runs_rules_version_history_idx;
ALTER TABLE battle_runs DROP COLUMN rules_version;
