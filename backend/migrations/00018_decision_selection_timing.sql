-- Freezes reasoning effort and records observable decision lifecycle timestamps.

-- +goose Up
ALTER TABLE battle_decisions
 ADD COLUMN reasoning_effort text NOT NULL DEFAULT '',
 ADD COLUMN claimed_at timestamptz,
 ADD COLUMN context_served_at timestamptz,
 ADD COLUMN runner_completed_at timestamptz;

-- Record claims centrally, including older workers. On a reclaimed lease, timing
-- describes the latest execution; created_at still includes the entire wait.
-- +goose StatementBegin
CREATE FUNCTION record_decision_claim_time() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.status = 'running' AND (OLD.status <> 'running' OR NEW.lease_expires_at IS DISTINCT FROM OLD.lease_expires_at) THEN
  NEW.claimed_at := clock_timestamp();
  NEW.context_served_at := NULL;
  NEW.runner_completed_at := NULL;
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER battle_decision_claim_time BEFORE UPDATE ON battle_decisions
 FOR EACH ROW EXECUTE FUNCTION record_decision_claim_time();

-- +goose Down
DROP TRIGGER battle_decision_claim_time ON battle_decisions;
DROP FUNCTION record_decision_claim_time();
ALTER TABLE battle_decisions DROP COLUMN reasoning_effort, DROP COLUMN claimed_at,
 DROP COLUMN context_served_at, DROP COLUMN runner_completed_at;
