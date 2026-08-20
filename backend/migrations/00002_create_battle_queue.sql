-- +goose Up
CREATE TABLE battle_runs (
    id uuid PRIMARY KEY,
    scenario_id text NOT NULL,
    scenario_digest text NOT NULL,
    scenario_snapshot jsonb NOT NULL,
    requested_ticks bigint NOT NULL CHECK (requested_ticks BETWEEN 1 AND 10000),
    current_tick bigint NOT NULL DEFAULT 0 CHECK (current_tick >= 0),
    status text NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued', 'running', 'completed', 'failed')),
    attempt integer NOT NULL DEFAULT 0 CHECK (attempt >= 0),
    lease_owner text,
    lease_expires_at timestamptz,
    result jsonb,
    error_code text,
    error_message text,
    created_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz,
    completed_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((lease_owner IS NULL) = (lease_expires_at IS NULL)),
    CHECK (status = 'running' OR lease_owner IS NULL)
);

CREATE INDEX battle_runs_claim_idx
ON battle_runs (status, lease_expires_at, created_at, id)
WHERE status IN ('queued', 'running');

CREATE TABLE battle_events (
    battle_id uuid NOT NULL REFERENCES battle_runs(id) ON DELETE CASCADE,
    attempt integer NOT NULL CHECK (attempt > 0),
    sequence bigint NOT NULL CHECK (sequence > 0),
    tick bigint NOT NULL CHECK (tick >= 0),
    type text NOT NULL CHECK (length(trim(type)) > 0),
    payload jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (battle_id, attempt, sequence)
);

CREATE INDEX battle_events_cursor_idx
ON battle_events (battle_id, attempt, sequence);

-- +goose Down
DROP TABLE battle_events;
DROP TABLE battle_runs;
