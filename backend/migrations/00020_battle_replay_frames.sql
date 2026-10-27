-- Stores a resumable engine checkpoint and its tick's events after every decision
-- window, so a finished battle replays from the nearest window instead of tick zero.

-- +goose Up
CREATE TABLE battle_replay_frames (
    battle_id uuid NOT NULL REFERENCES battle_runs(id) ON DELETE CASCADE,
    attempt integer NOT NULL CHECK (attempt > 0),
    tick bigint NOT NULL CHECK (tick >= 0),
    checkpoint jsonb NOT NULL CHECK (jsonb_typeof(checkpoint) = 'object'),
    events jsonb NOT NULL CHECK (jsonb_typeof(events) = 'array'),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (battle_id, attempt, tick)
);

-- +goose Down
DROP TABLE battle_replay_frames;
