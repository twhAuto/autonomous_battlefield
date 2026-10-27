-- Counts graceful-shutdown requeues per battle so they refund the processing attempt
-- they interrupted; only crashed or lease-expired attempts spend engine.maxAttempts.
-- Attempt numbers still rise on every claim, keeping each attempt's events separate.

-- +goose Up
ALTER TABLE battle_runs
    ADD COLUMN graceful_requeues integer NOT NULL DEFAULT 0,
    ADD CHECK (graceful_requeues BETWEEN 0 AND attempt);

-- +goose Down
-- Unfinished battles fall back to charging every claim, as before this migration.
ALTER TABLE battle_runs DROP COLUMN graceful_requeues;
