// Claims queued or expired battle work and enforces worker lease ownership for heartbeats,
// failure, and requeue writes.

package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/twh09/autonomous_battlefield/backend/internal/battle/queue"
	"github.com/twh09/autonomous_battlefield/backend/internal/observability/tracing"
)

// FailExhaustedBattles fails queued or lease-expired battles that have used every
// attempt, so they end as attempts_exhausted instead of waiting forever. ClaimBattle
// never claims them; the engine runs this before each claim.
func (r *Postgres) FailExhaustedBattles(ctx context.Context, maxAttempts int) error {
	_, err := r.db.Exec(ctx, `
		UPDATE battle_runs
		SET status = 'failed', error_code = 'attempts_exhausted',
		    error_message = 'maximum processing attempts exhausted',
		    lease_owner = NULL, lease_expires_at = NULL,
		    completed_at = now(), updated_at = now()
		WHERE ((status = 'queued') OR (status = 'running' AND lease_expires_at <= now()))
		  AND NOT paused
		  AND attempt >= $1
	`, maxAttempts)
	if err != nil {
		return fmt.Errorf("fail exhausted battles: %w", err)
	}
	return nil
}

func (r *Postgres) ClaimBattle(ctx context.Context, workerID string, leaseDuration time.Duration, maxAttempts int) (*queue.Job, error) {
	ctx, span := tracing.StartDatabaseSpan(ctx, "claim_battle")
	defer span.End()
	var job queue.Job
	var snapshot []byte
	err := r.db.QueryRow(ctx, `
		WITH candidate AS (
			SELECT id
			FROM battle_runs
			WHERE NOT paused
			  AND ((status = 'queued' AND attempt < $3)
			   OR (status = 'running' AND lease_expires_at <= now() AND attempt < $3))
			ORDER BY created_at ASC, id ASC
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE battle_runs AS battle
		SET status = 'running', attempt = attempt + 1, current_tick = 0, current_state = initial_state,
		    lease_owner = $1, lease_expires_at = now() + $2::interval,
		    started_at = COALESCE(started_at, now()), completed_at = NULL,
		    result = NULL, error_code = NULL, error_message = NULL, updated_at = now()
		FROM candidate
		WHERE battle.id = candidate.id
		RETURNING battle.id, battle.attempt, battle.scenario_snapshot, battle.requested_ticks,
		          battle.traceparent, battle.tracestate
	`, workerID, leaseDuration.String(), maxAttempts).Scan(
		&job.Lease.BattleID, &job.Lease.Attempt, &snapshot, &job.RequestedTicks,
		&job.Traceparent, &job.Tracestate,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, queue.ErrNoBattleAvailable
	}
	if err != nil {
		return nil, fmt.Errorf("claim battle: %w", err)
	}
	job.Lease.Owner = workerID
	if err := json.Unmarshal(snapshot, &job.Scenario); err != nil {
		return nil, fmt.Errorf("decode claimed battle %q scenario: %w", job.Lease.BattleID, err)
	}
	return &job, nil
}

func (r *Postgres) HeartbeatBattle(ctx context.Context, lease queue.Lease, leaseDuration time.Duration) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE battle_runs
		SET lease_expires_at = now() + $4::interval, updated_at = now()
		WHERE id = $1 AND status = 'running' AND attempt = $2 AND lease_owner = $3
	`, lease.BattleID, lease.Attempt, lease.Owner, leaseDuration.String())
	if err != nil {
		return fmt.Errorf("heartbeat battle %q: %w", lease.BattleID, err)
	}
	if tag.RowsAffected() == 0 {
		return queue.ErrLeaseLost
	}
	return nil
}

// BattlePaused reports operator control state only while the caller still owns
// the active lease. This prevents a stale worker from waiting on a battle that
// has been stopped or reclaimed.
func (r *Postgres) BattlePaused(ctx context.Context, lease queue.Lease) (bool, error) {
	var paused bool
	err := r.db.QueryRow(ctx, `
		SELECT paused
		FROM battle_runs
		WHERE id = $1 AND status = 'running' AND attempt = $2 AND lease_owner = $3
	`, lease.BattleID, lease.Attempt, lease.Owner).Scan(&paused)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, queue.ErrLeaseLost
	}
	if err != nil {
		return false, fmt.Errorf("read battle %q pause state: %w", lease.BattleID, err)
	}
	return paused, nil
}

func (r *Postgres) FailBattle(ctx context.Context, lease queue.Lease, code, message string) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE battle_runs
		SET status = 'failed', paused = false, error_code = $4, error_message = $5,
		    lease_owner = NULL, lease_expires_at = NULL,
		    completed_at = now(), updated_at = now()
		WHERE id = $1 AND status = 'running' AND attempt = $2 AND lease_owner = $3
	`, lease.BattleID, lease.Attempt, lease.Owner, code, message)
	if err != nil {
		return fmt.Errorf("fail battle %q: %w", lease.BattleID, err)
	}
	if tag.RowsAffected() == 0 {
		return queue.ErrLeaseLost
	}
	return nil
}

func (r *Postgres) RequeueBattle(ctx context.Context, lease queue.Lease) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE battle_runs
		SET status = 'queued', current_tick = 0, current_state = initial_state,
		    lease_owner = NULL, lease_expires_at = NULL, updated_at = now()
		WHERE id = $1 AND status = 'running' AND attempt = $2 AND lease_owner = $3
	`, lease.BattleID, lease.Attempt, lease.Owner)
	if err != nil {
		return fmt.Errorf("requeue battle %q: %w", lease.BattleID, err)
	}
	if tag.RowsAffected() == 0 {
		return queue.ErrLeaseLost
	}
	return nil
}
