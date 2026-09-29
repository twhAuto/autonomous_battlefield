-- Drops battle_events_cursor_idx: it indexes exactly the primary-key columns
-- (battle_id, attempt, sequence), so battle_events_pkey already serves every event
-- page query, and the duplicate only doubled index writes on each event insert.
-- A plain DROP takes a brief exclusive lock on battle_events; the table is small
-- and the migration job runs before the engine rolls out.

-- +goose Up
DROP INDEX battle_events_cursor_idx;

-- +goose Down
CREATE INDEX battle_events_cursor_idx ON battle_events (battle_id, attempt, sequence);
