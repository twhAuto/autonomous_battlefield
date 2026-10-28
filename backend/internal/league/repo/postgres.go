// Stores leagues, entrants and fixtures in PostgreSQL, joins each fixture's current
// battle outcome on read, and serializes scheduling and cancellation across API
// replicas with one transaction advisory lock.

package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/league"
	leagueservice "github.com/twh09/autonomous_battlefield/backend/internal/league/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/postgres"
)

// schedulerLock serializes league scheduling passes and cancellations.
const schedulerLock = 741928320

type Postgres struct {
	db postgres.Pool
}

func NewPostgres(db postgres.Pool) *Postgres {
	return &Postgres{db: db}
}

func (r *Postgres) CreateLeague(ctx context.Context, l league.League) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	_, err = tx.Exec(ctx, `
		INSERT INTO leagues (id, owner_id, name, request_digest, scenario_ids, requested_ticks, tick_duration, decision_interval_ticks, group_count)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		l.ID, l.OwnerID, l.Name, l.RequestDigest, l.Settings.ScenarioIDs, l.Settings.TickCount, l.Settings.TickDuration, l.Settings.DecisionIntervalTicks, l.GroupCount)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return fmt.Errorf("%w: %q", leagueservice.ErrLeagueAlreadyExists, l.ID)
		}
		return fmt.Errorf("create league %q: %w", l.ID, err)
	}
	for _, entrant := range l.Entrants {
		controller, err := json.Marshal(entrant.Controller)
		if err != nil {
			return fmt.Errorf("marshal entrant controller: %w", err)
		}
		if _, err = tx.Exec(ctx, `
			INSERT INTO league_entrants (league_id, entrant_index, group_index, faction_id, faction_name, controller)
			VALUES ($1, $2, $3, $4, $5, $6)`, l.ID, entrant.Index, entrant.Group, entrant.FactionID, entrant.FactionName, controller); err != nil {
			return fmt.Errorf("create league %q entrant %d: %w", l.ID, entrant.Index, err)
		}
	}
	if err := insertFixtures(ctx, tx, l.ID, l.Fixtures); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// insertFixtures ignores fixtures that already exist: their IDs are derived, so a
// repeated write is the same fixture.
func insertFixtures(ctx context.Context, db postgres.DBTX, leagueID string, fixtures []league.Fixture) error {
	for _, f := range fixtures {
		if _, err := db.Exec(ctx, `
			INSERT INTO league_fixtures (id, league_id, sequence, stage, group_index, round, tie, leg, home_entrant, away_entrant, scenario_id, status, attempts, battle_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
			ON CONFLICT (id) DO NOTHING`,
			f.ID, leagueID, f.Sequence, f.Stage, f.Group, f.Round, f.Tie, f.Leg, f.Home, f.Away, f.ScenarioID, f.Status, f.Attempts, f.BattleID); err != nil {
			return fmt.Errorf("create league %q fixture %d: %w", leagueID, f.Sequence, err)
		}
	}
	return nil
}

func (r *Postgres) GetLeague(ctx context.Context, id string) (*league.League, error) {
	return loadLeague(ctx, r.db, id)
}

func loadLeague(ctx context.Context, db postgres.DBTX, id string) (*league.League, error) {
	l := league.League{}
	err := db.QueryRow(ctx, `
		SELECT id, owner_id, name, status, request_digest, scenario_ids, requested_ticks, tick_duration, decision_interval_ticks,
		       group_count, champion_entrant, created_at, completed_at
		FROM leagues WHERE id = $1`, id).Scan(
		&l.ID, &l.OwnerID, &l.Name, &l.Status, &l.RequestDigest, &l.Settings.ScenarioIDs, &l.Settings.TickCount, &l.Settings.TickDuration,
		&l.Settings.DecisionIntervalTicks, &l.GroupCount, &l.Champion, &l.CreatedAt, &l.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, leagueservice.ErrLeagueNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get league %q: %w", id, err)
	}
	if l.Entrants, err = loadEntrants(ctx, db, id); err != nil {
		return nil, err
	}
	if l.Fixtures, err = loadFixtures(ctx, db, id); err != nil {
		return nil, err
	}
	return &l, nil
}

func loadEntrants(ctx context.Context, db postgres.DBTX, id string) ([]league.Entrant, error) {
	rows, err := db.Query(ctx, `
		SELECT entrant_index, group_index, faction_id, faction_name, controller
		FROM league_entrants WHERE league_id = $1 ORDER BY entrant_index`, id)
	if err != nil {
		return nil, fmt.Errorf("list league %q entrants: %w", id, err)
	}
	defer rows.Close()
	entrants := []league.Entrant{}
	for rows.Next() {
		var entrant league.Entrant
		var controller []byte
		if err := rows.Scan(&entrant.Index, &entrant.Group, &entrant.FactionID, &entrant.FactionName, &controller); err != nil {
			return nil, fmt.Errorf("scan league %q entrant: %w", id, err)
		}
		if err := json.Unmarshal(controller, &entrant.Controller); err != nil {
			return nil, fmt.Errorf("decode league %q entrant controller: %w", id, err)
		}
		entrants = append(entrants, entrant)
	}
	return entrants, rows.Err()
}

func loadFixtures(ctx context.Context, db postgres.DBTX, id string) ([]league.Fixture, error) {
	rows, err := db.Query(ctx, `
		SELECT f.id, f.sequence, f.stage, f.group_index, f.round, f.tie, f.leg, f.home_entrant, f.away_entrant, f.scenario_id,
		       f.status, f.attempts, f.battle_id, b.status, b.result
		FROM league_fixtures f
		LEFT JOIN battle_runs b ON b.id = f.battle_id
		WHERE f.league_id = $1 ORDER BY f.sequence`, id)
	if err != nil {
		return nil, fmt.Errorf("list league %q fixtures: %w", id, err)
	}
	defer rows.Close()
	fixtures := []league.Fixture{}
	for rows.Next() {
		f, err := scanFixture(rows)
		if err != nil {
			return nil, fmt.Errorf("scan league %q fixture: %w", id, err)
		}
		fixtures = append(fixtures, f)
	}
	return fixtures, rows.Err()
}

func scanFixture(rows pgx.Rows) (league.Fixture, error) {
	var f league.Fixture
	var battleStatus *string
	var result []byte
	if err := rows.Scan(&f.ID, &f.Sequence, &f.Stage, &f.Group, &f.Round, &f.Tie, &f.Leg, &f.Home, &f.Away, &f.ScenarioID,
		&f.Status, &f.Attempts, &f.BattleID, &battleStatus, &result); err != nil {
		return f, err
	}
	if battleStatus == nil {
		return f, nil
	}
	f.Battle = &league.BattleOutcome{Status: *battleStatus}
	if len(result) == 0 || string(result) == "null" {
		return f, nil
	}
	var decoded game.Result
	if err := json.Unmarshal(result, &decoded); err != nil {
		return f, fmt.Errorf("decode battle result: %w", err)
	}
	f.Battle.WinnerFactionID, f.Battle.Reason = decoded.WinnerFactionID, decoded.Reason
	f.Battle.Factions = make(map[string]league.FactionScore, len(decoded.Factions))
	for _, faction := range decoded.Factions {
		f.Battle.Factions[faction.FactionID] = league.FactionScore{CastleHealth: faction.CastleHealth, DamageDealt: faction.DamageDealt}
	}
	return f, nil
}

func (r *Postgres) ListLeagues(ctx context.Context, limit int) ([]league.Summary, error) {
	rows, err := r.db.Query(ctx, `
		SELECT l.id, l.owner_id, l.name, l.status, l.group_count, l.champion_entrant, e.faction_name,
		       (SELECT count(*) FROM league_fixtures f WHERE f.league_id = l.id AND f.status IN ('completed', 'void', 'cancelled')),
		       l.created_at, l.completed_at
		FROM leagues l
		LEFT JOIN league_entrants e ON e.league_id = l.id AND e.entrant_index = l.champion_entrant
		ORDER BY l.created_at DESC, l.id DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list leagues: %w", err)
	}
	defer rows.Close()
	summaries := []league.Summary{}
	for rows.Next() {
		var s league.Summary
		if err := rows.Scan(&s.ID, &s.OwnerID, &s.Name, &s.Status, &s.GroupCount, &s.Champion, &s.ChampionName, &s.Settled, &s.CreatedAt, &s.CompletedAt); err != nil {
			return nil, fmt.Errorf("scan league: %w", err)
		}
		summaries = append(summaries, s)
	}
	return summaries, rows.Err()
}

func (r *Postgres) CancelLeague(ctx context.Context, id string) (bool, []string, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return false, nil, err
	}
	defer rollback(ctx, tx)
	// Wait for any scheduling pass, so none starts a battle for this league afterwards.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, schedulerLock); err != nil {
		return false, nil, fmt.Errorf("lock league scheduler: %w", err)
	}
	var status string
	err = tx.QueryRow(ctx, `SELECT status FROM leagues WHERE id = $1 FOR UPDATE`, id).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil, leagueservice.ErrLeagueNotFound
	}
	if err != nil {
		return false, nil, fmt.Errorf("get league %q: %w", id, err)
	}
	switch status {
	case league.StatusCancelled:
		return false, nil, nil
	case league.StatusCompleted:
		return false, nil, leagueservice.ErrLeagueNotActive
	}
	if _, err := tx.Exec(ctx, `UPDATE leagues SET status = 'cancelled', completed_at = now(), updated_at = now() WHERE id = $1`, id); err != nil {
		return false, nil, fmt.Errorf("cancel league %q: %w", id, err)
	}
	rows, err := tx.Query(ctx, `
		UPDATE league_fixtures SET status = 'cancelled', updated_at = now()
		WHERE league_id = $1 AND status IN ('pending', 'running')
		RETURNING battle_id`, id)
	if err != nil {
		return false, nil, fmt.Errorf("cancel league %q fixtures: %w", id, err)
	}
	battles, err := pgx.CollectRows(rows, pgx.RowTo[*string])
	if err != nil {
		return false, nil, fmt.Errorf("cancel league %q fixtures: %w", id, err)
	}
	running := []string{}
	for _, battleID := range battles {
		if battleID != nil {
			running = append(running, *battleID)
		}
	}
	return true, running, tx.Commit(ctx)
}

func (r *Postgres) Schedule(ctx context.Context, fn func(context.Context, leagueservice.SchedulerStore) error) (bool, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer rollback(ctx, tx)
	var locked bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1)`, schedulerLock).Scan(&locked); err != nil {
		return false, fmt.Errorf("lock league scheduler: %w", err)
	}
	if !locked {
		return false, nil
	}
	if err := fn(ctx, schedulerStore{tx: tx}); err != nil {
		return true, err
	}
	return true, tx.Commit(ctx)
}

// schedulerStore runs inside a scheduling pass's transaction.
type schedulerStore struct {
	tx pgx.Tx
}

func (s schedulerStore) RunningLeagues(ctx context.Context) ([]league.League, error) {
	rows, err := s.tx.Query(ctx, `SELECT id FROM leagues WHERE status = 'running' ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list running leagues: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("list running leagues: %w", err)
	}
	leagues := make([]league.League, 0, len(ids))
	for _, id := range ids {
		l, err := loadLeague(ctx, s.tx, id)
		if err != nil {
			return nil, err
		}
		leagues = append(leagues, *l)
	}
	return leagues, nil
}

func (s schedulerStore) SaveFixture(ctx context.Context, f league.Fixture) error {
	_, err := s.tx.Exec(ctx, `
		UPDATE league_fixtures SET status = $2, attempts = $3, battle_id = $4, updated_at = now()
		WHERE id = $1`, f.ID, f.Status, f.Attempts, f.BattleID)
	if err != nil {
		return fmt.Errorf("save league fixture %q: %w", f.ID, err)
	}
	return nil
}

func (s schedulerStore) AddFixtures(ctx context.Context, leagueID string, fixtures []league.Fixture) error {
	return insertFixtures(ctx, s.tx, leagueID, fixtures)
}

func (s schedulerStore) CompleteLeague(ctx context.Context, leagueID string, champion int) error {
	_, err := s.tx.Exec(ctx, `
		UPDATE leagues SET status = 'completed', champion_entrant = $2, completed_at = now(), updated_at = now()
		WHERE id = $1 AND status = 'running'`, leagueID, champion)
	if err != nil {
		return fmt.Errorf("complete league %q: %w", leagueID, err)
	}
	return nil
}

func (s schedulerStore) EngineBusy(ctx context.Context) (bool, error) {
	var busy bool
	if err := s.tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM battle_runs WHERE status IN ('queued', 'running'))`).Scan(&busy); err != nil {
		return false, fmt.Errorf("check engine queue: %w", err)
	}
	return busy, nil
}

func (s schedulerStore) BattleExists(ctx context.Context, id string) (bool, error) {
	var exists bool
	if err := s.tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM battle_runs WHERE id = $1)`, id).Scan(&exists); err != nil {
		return false, fmt.Errorf("check battle %q: %w", id, err)
	}
	return exists, nil
}

func rollback(ctx context.Context, tx pgx.Tx) {
	_ = tx.Rollback(context.WithoutCancel(ctx))
}
