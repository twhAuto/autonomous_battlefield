// Persists battle snapshots and implements status, pause, stop, and event reads for API-
// facing battle operations.

package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/telemetry"
)

func (r *Postgres) CreateBattle(ctx context.Context, params battleservice.CreateBattleParams) error {
	ctx, span := telemetry.StartDatabaseSpan(ctx, "create_battle")
	defer span.End()
	snapshot, err := json.Marshal(params.ScenarioSnapshot)
	if err != nil {
		return fmt.Errorf("marshal battle scenario: %w", err)
	}
	initialState, err := json.Marshal(params.InitialState)
	if err != nil {
		return fmt.Errorf("marshal battle initial state: %w", err)
	}
	_, err = r.db.Exec(ctx, `
		INSERT INTO battle_runs (id, scenario_id, scenario_digest, scenario_snapshot, requested_ticks, initial_state, current_state, traceparent, tracestate, owner_id)
		VALUES ($1, $2, $3, $4, $5, $6, $6, $7, $8, $9)
	`, params.ID, params.ScenarioID, params.ScenarioDigest, snapshot, params.RequestedTicks, initialState, params.Traceparent, params.Tracestate, authn.Owner(ctx))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "P0429" {
			return battleservice.ErrLimited
		}
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
		       initial_state, current_state, current_tick, status, paused, attempt, result, error_code, error_message,
		       created_at, started_at, completed_at, owner_id
		FROM battle_runs
		WHERE id = $1
	`, id).Scan(
		&run.ID, &run.ScenarioID, &run.ScenarioDigest, &snapshot, &run.RequestedTicks, &initialState, &currentState,
		&run.CurrentTick, &run.Status, &run.Paused, &run.Attempt, &result, &run.ErrorCode, &run.ErrorMessage,
		&run.CreatedAt, &run.StartedAt, &run.CompletedAt, &run.OwnerID,
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

func (r *Postgres) StopBattle(ctx context.Context, id string) error {
	var stoppedStatus, previousStatus string
	var stoppedDecisions int
	err := r.db.QueryRow(ctx, `
		WITH target AS MATERIALIZED (
			SELECT status FROM battle_runs WHERE id = $1
		), stopped AS (
			UPDATE battle_runs
			SET status = 'stopped', paused = false, lease_owner = NULL, lease_expires_at = NULL,
			    completed_at = now(), updated_at = now()
			WHERE id = $1 AND status IN ('queued', 'running')
			RETURNING id, status
		), stopped_decisions AS (
			UPDATE battle_decisions AS decision
			SET status = 'failed', error_category = 'battle_stopped',
			    lease_owner = NULL, lease_expires_at = NULL, resolved_at = now()
			FROM stopped
			WHERE decision.battle_id = stopped.id AND decision.status IN ('pending', 'running')
			RETURNING decision.id
		)
		SELECT COALESCE((SELECT status FROM stopped), ''),
		       COALESCE((SELECT status FROM target), ''),
		       (SELECT count(*) FROM stopped_decisions)
	`, id).Scan(&stoppedStatus, &previousStatus, &stoppedDecisions)
	if err != nil {
		return fmt.Errorf("stop battle %q: %w", id, err)
	}
	if previousStatus == "" {
		return battleservice.ErrBattleNotFound
	}
	if stoppedStatus == "" {
		return fmt.Errorf("%w: battle %q is %s", battleservice.ErrBattleNotActive, id, previousStatus)
	}
	return nil
}

func (r *Postgres) SetBattlePaused(ctx context.Context, id string, paused bool) error {
	var previousStatus string
	err := r.db.QueryRow(ctx, `
		WITH target AS MATERIALIZED (
			SELECT status FROM battle_runs WHERE id = $1
		), changed AS (
			UPDATE battle_runs
			SET paused = $2, updated_at = now()
			WHERE id = $1 AND status IN ('queued', 'running')
			RETURNING id
		)
		SELECT COALESCE((SELECT status FROM target), '')
	`, id, paused).Scan(&previousStatus)
	if err != nil {
		return fmt.Errorf("set battle %q paused=%t: %w", id, paused, err)
	}
	if previousStatus == "" {
		return battleservice.ErrBattleNotFound
	}
	if previousStatus != "queued" && previousStatus != "running" {
		return fmt.Errorf("%w: battle %q is %s", battleservice.ErrBattleNotActive, id, previousStatus)
	}
	return nil
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
