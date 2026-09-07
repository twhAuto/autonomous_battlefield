-- Persists W3C trace context on battle and decision rows so worker processes can continue
-- request traces.

-- +goose Up
ALTER TABLE battle_runs
    ADD COLUMN traceparent text NOT NULL DEFAULT '',
    ADD COLUMN tracestate text NOT NULL DEFAULT '';

ALTER TABLE battle_decisions
    ADD COLUMN traceparent text NOT NULL DEFAULT '',
    ADD COLUMN tracestate text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE battle_decisions
    DROP COLUMN tracestate,
    DROP COLUMN traceparent;

ALTER TABLE battle_runs
    DROP COLUMN tracestate,
    DROP COLUMN traceparent;
