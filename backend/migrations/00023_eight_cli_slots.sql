-- Lets CLI concurrency reach eight, so both CLI sides of four concurrent battles can
-- decide at once. The default stays four.
-- +goose Up
ALTER TABLE runtime_policy DROP CONSTRAINT runtime_policy_cli_concurrency_check,
    ADD CONSTRAINT runtime_policy_cli_concurrency_check CHECK (cli_concurrency BETWEEN 1 AND 8);

-- +goose Down
UPDATE runtime_policy SET cli_concurrency = 4 WHERE cli_concurrency > 4;
ALTER TABLE runtime_policy DROP CONSTRAINT runtime_policy_cli_concurrency_check,
    ADD CONSTRAINT runtime_policy_cli_concurrency_check CHECK (cli_concurrency BETWEEN 1 AND 4);
