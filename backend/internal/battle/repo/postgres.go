package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/twh09/autonomous_battlefield/backend/internal/battle/engine/worker"
	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/server/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/postgres"
)

type Postgres struct {
	db postgres.DBTX
}

func NewPostgres(db postgres.DBTX) *Postgres {
	return &Postgres{db: db}
}

func (r *Postgres) CreateBattle(ctx context.Context, params battleservice.CreateBattleParams) error {
	snapshot, err := json.Marshal(params.ScenarioSnapshot)
	if err != nil {
		return fmt.Errorf("marshal battle scenario: %w", err)
	}
	initialState, err := json.Marshal(params.InitialState)
	if err != nil {
		return fmt.Errorf("marshal battle initial state: %w", err)
	}
	_, err = r.db.Exec(ctx, `
		INSERT INTO battle_runs (id, scenario_id, scenario_digest, scenario_snapshot, requested_ticks, initial_state, current_state)
		VALUES ($1, $2, $3, $4, $5, $6, $6)
	`, params.ID, params.ScenarioID, params.ScenarioDigest, snapshot, params.RequestedTicks, initialState)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return fmt.Errorf("%w: %q", battleservice.ErrBattleAlreadyExists, params.ID)
		}
		return fmt.Errorf("create battle %q: %w", params.ID, err)
	}
	return nil
}

func (r *Postgres) GetBattle(ctx context.Context, id string) (*battleservice.BattleRun, error) {
	var run battleservice.BattleRun
	var snapshot []byte
	var initialState, currentState []byte
	var result []byte
	err := r.db.QueryRow(ctx, `
		SELECT id, scenario_id, scenario_digest, scenario_snapshot, requested_ticks,
		       initial_state, current_state, current_tick, status, attempt, result, error_code, error_message,
		       created_at, started_at, completed_at
		FROM battle_runs
		WHERE id = $1
	`, id).Scan(
		&run.ID, &run.ScenarioID, &run.ScenarioDigest, &snapshot, &run.RequestedTicks, &initialState, &currentState,
		&run.CurrentTick, &run.Status, &run.Attempt, &result, &run.ErrorCode, &run.ErrorMessage,
		&run.CreatedAt, &run.StartedAt, &run.CompletedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, battleservice.ErrBattleNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get battle %q: %w", id, err)
	}
	if err := json.Unmarshal(snapshot, &run.ScenarioSnapshot); err != nil {
		return nil, fmt.Errorf("decode battle %q scenario: %w", id, err)
	}
	if err := json.Unmarshal(initialState, &run.InitialState); err != nil {
		return nil, fmt.Errorf("decode battle %q initial state: %w", id, err)
	}
	if err := json.Unmarshal(currentState, &run.CurrentState); err != nil {
		return nil, fmt.Errorf("decode battle %q current state: %w", id, err)
	}
	if len(result) > 0 && string(result) != "null" {
		var decoded game.Result
		if err := json.Unmarshal(result, &decoded); err != nil {
			return nil, fmt.Errorf("decode battle %q result: %w", id, err)
		}
		run.Result = &decoded
	}
	return &run, nil
}

func (r *Postgres) ListBattleEvents(ctx context.Context, battleID string, attempt int, after uint64, limit int, latest bool) ([]battleservice.StoredBattleEvent, error) {
	query := `
		SELECT attempt, sequence, tick, type, payload, created_at
		FROM battle_events
		WHERE battle_id = $1 AND attempt = $2 AND sequence > $3
		ORDER BY sequence ASC
		LIMIT $4
	`
	arguments := []any{battleID, attempt, after, limit}
	if latest {
		query = `
			SELECT attempt, sequence, tick, type, payload, created_at
			FROM (
				SELECT attempt, sequence, tick, type, payload, created_at
				FROM battle_events
				WHERE battle_id = $1 AND attempt = $2
				ORDER BY sequence DESC
				LIMIT $3
			) AS latest_events
			ORDER BY sequence ASC
		`
		arguments = []any{battleID, attempt, limit}
	}
	rows, err := r.db.Query(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list battle %q events: %w", battleID, err)
	}
	defer rows.Close()

	events := make([]battleservice.StoredBattleEvent, 0)
	for rows.Next() {
		var event battleservice.StoredBattleEvent
		var payload []byte
		if err := rows.Scan(&event.Attempt, &event.Sequence, &event.Tick, &event.Type, &payload, &event.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan battle event: %w", err)
		}
		if err := json.Unmarshal(payload, &event.Data); err != nil {
			return nil, fmt.Errorf("decode battle event payload: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate battle events: %w", err)
	}
	return events, nil
}

func (r *Postgres) ClaimBattle(ctx context.Context, workerID string, leaseDuration time.Duration, maxAttempts int) (*worker.BattleJob, error) {
	_, err := r.db.Exec(ctx, `
		UPDATE battle_runs
		SET status = 'failed', error_code = 'attempts_exhausted',
		    error_message = 'maximum processing attempts exhausted',
		    lease_owner = NULL, lease_expires_at = NULL,
		    completed_at = now(), updated_at = now()
		WHERE ((status = 'queued') OR (status = 'running' AND lease_expires_at <= now()))
		  AND attempt >= $1
	`, maxAttempts)
	if err != nil {
		return nil, fmt.Errorf("fail exhausted battles: %w", err)
	}

	var job worker.BattleJob
	var snapshot []byte
	err = r.db.QueryRow(ctx, `
		WITH candidate AS (
			SELECT id
			FROM battle_runs
			WHERE (status = 'queued' AND attempt < $3)
			   OR (status = 'running' AND lease_expires_at <= now() AND attempt < $3)
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
		RETURNING battle.id, battle.attempt, battle.scenario_snapshot, battle.requested_ticks
	`, workerID, leaseDuration.String(), maxAttempts).Scan(
		&job.Lease.BattleID, &job.Lease.Attempt, &snapshot, &job.RequestedTicks,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, worker.ErrNoBattleAvailable
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

func (r *Postgres) HeartbeatBattle(ctx context.Context, lease worker.BattleLease, leaseDuration time.Duration) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE battle_runs
		SET lease_expires_at = now() + $4::interval, updated_at = now()
		WHERE id = $1 AND status = 'running' AND attempt = $2 AND lease_owner = $3
	`, lease.BattleID, lease.Attempt, lease.Owner, leaseDuration.String())
	if err != nil {
		return fmt.Errorf("heartbeat battle %q: %w", lease.BattleID, err)
	}
	if tag.RowsAffected() == 0 {
		return worker.ErrBattleLeaseLost
	}
	return nil
}

func (r *Postgres) SaveTick(ctx context.Context, lease worker.BattleLease, transition game.Transition, leaseDuration time.Duration) error {
	events, err := json.Marshal(transition.Events)
	if err != nil {
		return fmt.Errorf("marshal tick events: %w", err)
	}
	state, err := json.Marshal(transition.State)
	if err != nil {
		return fmt.Errorf("marshal tick state: %w", err)
	}
	var owned bool
	err = r.db.QueryRow(ctx, `
		WITH owned AS (
			UPDATE battle_runs
			SET current_tick = $4, current_state = $5, lease_expires_at = now() + $6::interval, updated_at = now()
			WHERE id = $1 AND status = 'running' AND attempt = $2 AND lease_owner = $3
			RETURNING id
		), inserted AS (
			INSERT INTO battle_events (battle_id, attempt, sequence, tick, type, payload)
			SELECT $1, $2, event.sequence, event.tick, event.type, event.data
			FROM owned, jsonb_to_recordset($7::jsonb)
			     AS event(sequence bigint, tick bigint, type text, data jsonb)
			ON CONFLICT (battle_id, attempt, sequence) DO NOTHING
		)
		SELECT EXISTS (SELECT 1 FROM owned)
	`, lease.BattleID, lease.Attempt, lease.Owner, transition.Tick, state, leaseDuration.String(), events).Scan(&owned)
	if err != nil {
		return fmt.Errorf("save battle %q tick %d: %w", lease.BattleID, transition.Tick, err)
	}
	if !owned {
		return worker.ErrBattleLeaseLost
	}
	return nil
}

func (r *Postgres) CompleteBattle(ctx context.Context, lease worker.BattleLease, transition game.Transition) error {
	if !transition.Finished || transition.Result == nil {
		return errors.New("cannot complete battle without a finished transition")
	}
	events, err := json.Marshal(transition.Events)
	if err != nil {
		return fmt.Errorf("marshal completion events: %w", err)
	}
	result, err := json.Marshal(transition.Result)
	if err != nil {
		return fmt.Errorf("marshal battle result: %w", err)
	}
	state, err := json.Marshal(transition.State)
	if err != nil {
		return fmt.Errorf("marshal final state: %w", err)
	}
	var owned bool
	err = r.db.QueryRow(ctx, `
		WITH owned AS (
			UPDATE battle_runs
			SET status = 'completed', current_tick = $4, result = $5, current_state = $6,
			    lease_owner = NULL, lease_expires_at = NULL,
			    completed_at = now(), updated_at = now()
			WHERE id = $1 AND status = 'running' AND attempt = $2 AND lease_owner = $3
			RETURNING id
		), inserted AS (
			INSERT INTO battle_events (battle_id, attempt, sequence, tick, type, payload)
			SELECT $1, $2, event.sequence, event.tick, event.type, event.data
			FROM owned, jsonb_to_recordset($7::jsonb)
			     AS event(sequence bigint, tick bigint, type text, data jsonb)
			ON CONFLICT (battle_id, attempt, sequence) DO NOTHING
		)
		SELECT EXISTS (SELECT 1 FROM owned)
	`, lease.BattleID, lease.Attempt, lease.Owner, transition.Tick, result, state, events).Scan(&owned)
	if err != nil {
		return fmt.Errorf("complete battle %q: %w", lease.BattleID, err)
	}
	if !owned {
		return worker.ErrBattleLeaseLost
	}
	return nil
}

func (r *Postgres) FailBattle(ctx context.Context, lease worker.BattleLease, code, message string) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE battle_runs
		SET status = 'failed', error_code = $4, error_message = $5,
		    lease_owner = NULL, lease_expires_at = NULL,
		    completed_at = now(), updated_at = now()
		WHERE id = $1 AND status = 'running' AND attempt = $2 AND lease_owner = $3
	`, lease.BattleID, lease.Attempt, lease.Owner, code, message)
	if err != nil {
		return fmt.Errorf("fail battle %q: %w", lease.BattleID, err)
	}
	if tag.RowsAffected() == 0 {
		return worker.ErrBattleLeaseLost
	}
	return nil
}

func (r *Postgres) RequeueBattle(ctx context.Context, lease worker.BattleLease) error {
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
		return worker.ErrBattleLeaseLost
	}
	return nil
}
