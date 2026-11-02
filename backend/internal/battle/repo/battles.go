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
	"github.com/twh09/autonomous_battlefield/backend/internal/observability/tracing"
)

// BattleAllowance reads the role's allowances and the owner's counts with the queries
// the admission trigger uses, without its row lock, so the result can be stale by the
// time a battle is created.
func (r *Postgres) BattleAllowance(ctx context.Context, owner string, tester bool) (battleservice.Allowance, error) {
	ctx, span := tracing.StartDatabaseSpan(ctx, "battle_allowance")
	defer span.End()
	allowance := battleservice.Allowance{Limited: true}
	err := r.db.QueryRow(ctx, `
		SELECT CASE WHEN $2 THEN p.tester_daily_battles ELSE p.player_daily_battles END,
		       CASE WHEN $2 THEN p.tester_unfinished ELSE p.player_unfinished END,
		       p.global_unfinished,
		       (SELECT count(*) FROM battle_runs WHERE owner_id=$1 AND created_at >= date_trunc('day',now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),
		       (SELECT count(*) FROM battle_runs WHERE owner_id=$1 AND status IN ('queued','running')),
		       (SELECT count(*) FROM battle_runs WHERE status IN ('queued','running')),
		       (date_trunc('day',now() AT TIME ZONE 'UTC') + interval '1 day') AT TIME ZONE 'UTC'
		FROM runtime_policy p WHERE p.id
	`, owner, tester).Scan(&allowance.DailyLimit, &allowance.UnfinishedLimit, &allowance.ServiceUnfinishedLimit,
		&allowance.StartedToday, &allowance.Unfinished, &allowance.ServiceUnfinished, &allowance.ResetAt)
	if err != nil {
		return battleservice.Allowance{}, fmt.Errorf("read battle allowance: %w", err)
	}
	return allowance, nil
}

func (r *Postgres) CreateBattle(ctx context.Context, params battleservice.CreateBattleParams) error {
	ctx, span := tracing.StartDatabaseSpan(ctx, "create_battle")
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
		INSERT INTO battle_runs (id, scenario_id, scenario_digest, scenario_snapshot, requested_ticks, initial_state, current_state, traceparent, tracestate, owner_id, admission_exempt, tester_allowance, decision_budget_exempt)
		VALUES ($1, $2, $3, $4, $5, $6, $6, $7, $8, $9, $10, $11, $12)
	`, params.ID, params.ScenarioID, params.ScenarioDigest, snapshot, params.RequestedTicks, initialState, params.Traceparent, params.Tracestate, authn.Owner(ctx), params.AdmissionExempt, params.TesterAllowance, params.DecisionBudgetExempt)
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
	var league battleLeagueColumns
	err := r.db.QueryRow(ctx, `
		SELECT r.id, r.scenario_id, r.scenario_digest, r.scenario_snapshot, r.requested_ticks,
		       r.initial_state, r.current_state, r.current_tick, r.status, r.paused, r.attempt, r.result, r.error_code, r.error_message,
		       r.created_at, r.started_at, r.completed_at, r.owner_id,
		       l.id, l.name, f.id, f.stage, f.group_index, f.round, f.tie
		FROM battle_runs r
		LEFT JOIN league_fixtures f ON f.battle_id = r.id
		LEFT JOIN leagues l ON l.id = f.league_id
		WHERE r.id = $1
	`, id).Scan(
		&run.ID, &run.ScenarioID, &run.ScenarioDigest, &snapshot, &run.RequestedTicks, &initialState, &currentState,
		&run.CurrentTick, &run.Status, &run.Paused, &run.Attempt, &result, &run.ErrorCode, &run.ErrorMessage,
		&run.CreatedAt, &run.StartedAt, &run.CompletedAt, &run.OwnerID,
		&league.id, &league.name, &league.fixtureID, &league.stage, &league.group, &league.round, &league.tie,
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
	run.League = league.value()
	return &run, nil
}

// battleLeagueColumns scans the nullable league columns of a battle read.
type battleLeagueColumns struct {
	id, name, fixtureID, stage *string
	group, round, tie          *int
}

func (c battleLeagueColumns) value() *battleservice.BattleLeague {
	if c.id == nil || c.fixtureID == nil {
		return nil
	}
	league := &battleservice.BattleLeague{ID: *c.id, Name: *c.name, FixtureID: *c.fixtureID, Stage: *c.stage, Round: *c.round, Tie: *c.tie}
	if c.group != nil {
		letter := string(rune('A' + *c.group))
		league.Group = &letter
	}
	return league
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
	if stoppedStatus == "" && previousStatus != "stopped" {
		// An already-stopped battle is the desired outcome, so a retried stop
		// succeeds; stopping a battle that completed or failed stays a conflict.
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
