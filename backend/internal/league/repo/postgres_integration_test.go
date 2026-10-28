// Exercises league storage against PostgreSQL in a private schema, so the engine-idle
// check sees only this test's battles: creation and reads, fixture outcomes joined from
// battles, the scheduler lock, cancellation, and the league shown on a battle read.

package repo

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	battlerepo "github.com/twh09/autonomous_battlefield/backend/internal/battle/repo"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/league"
	leagueservice "github.com/twh09/autonomous_battlefield/backend/internal/league/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/postgres"
)

func privateDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	schema := fmt.Sprintf("league_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal("test database URL is not a URL")
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	if _, err := postgres.Migrate(ctx, parsed.String(), 10*time.Second); err != nil {
		t.Fatal(err)
	}
	db, err := postgres.Connect(ctx, parsed.String(), postgres.Options{MaxConnections: 4, MaxConnectionLifetime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return db
}

func uuid(t *testing.T) string {
	t.Helper()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	b[6], b[8] = (b[6]&0x0f)|0x40, (b[8]&0x3f)|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func sampleLeague(t *testing.T) league.League {
	t.Helper()
	interval := uint64(10)
	l := league.League{ID: uuid(t), OwnerID: "admin", Name: "Autumn cup", Status: league.StatusRunning, RequestDigest: "digest", GroupCount: 2,
		Settings: league.Settings{ScenarioIDs: []string{"frontier-siege", "castle-siege"}, TickCount: 500, DecisionIntervalTicks: &interval}}
	for i := range 8 {
		controller := game.ControllerConfig{Type: "model", ProfileID: "claude-subscription-local", ModelIdentifier: "sonnet", ReasoningEffort: "low"}
		l.Entrants = append(l.Entrants, league.Entrant{Index: i, Group: i % 2, FactionID: uuid(t), FactionName: fmt.Sprintf("Faction %d", i), Controller: controller})
	}
	l.Fixtures = league.GroupFixtures(l.ID, l.Entrants, 2, l.Settings.ScenarioIDs)
	return l
}

func insertBattle(t *testing.T, db *pgxpool.Pool, id string) {
	t.Helper()
	if _, err := db.Exec(context.Background(), `INSERT INTO battle_runs(id,scenario_id,scenario_digest,scenario_snapshot,requested_ticks,owner_id,initial_state,current_state,admission_exempt)
		VALUES($1,'frontier-siege','d','{}',500,'admin','{}','{}',true)`, id); err != nil {
		t.Fatal(err)
	}
}

func schedule(t *testing.T, repo *Postgres, fn func(context.Context, leagueservice.SchedulerStore) error) {
	t.Helper()
	ran, err := repo.Schedule(context.Background(), fn)
	if err != nil || !ran {
		t.Fatalf("Schedule() = %v, %v", ran, err)
	}
}

func TestLeagueRoundTripAgainstPostgres(t *testing.T) {
	db := privateDatabase(t)
	repo, ctx, l := NewPostgres(db), context.Background(), sampleLeague(t)
	if err := repo.CreateLeague(ctx, l); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateLeague(ctx, l); !errors.Is(err, leagueservice.ErrLeagueAlreadyExists) {
		t.Fatalf("duplicate err = %v", err)
	}
	got, err := repo.GetLeague(ctx, l.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != l.Name || got.Status != league.StatusRunning || got.RequestDigest != "digest" || got.GroupCount != 2 || got.Settings.TickCount != 500 ||
		*got.Settings.DecisionIntervalTicks != 10 || got.Settings.TickDuration != nil || len(got.Settings.ScenarioIDs) != 2 || got.Champion != nil || got.CompletedAt != nil {
		t.Fatalf("league = %+v", got)
	}
	if len(got.Entrants) != 8 || got.Entrants[5] != l.Entrants[5] {
		t.Fatalf("entrant = %+v, want %+v", got.Entrants[5], l.Entrants[5])
	}
	if len(got.Fixtures) != 24 || got.Fixtures[7].ID != l.Fixtures[7].ID || *got.Fixtures[7].Group != *l.Fixtures[7].Group || got.Fixtures[7].Battle != nil {
		t.Fatalf("fixture = %+v", got.Fixtures[7])
	}
	if _, err := repo.GetLeague(ctx, uuid(t)); !errors.Is(err, leagueservice.ErrLeagueNotFound) {
		t.Fatalf("missing err = %v", err)
	}
}

func TestSchedulerStoreAgainstPostgres(t *testing.T) {
	db := privateDatabase(t)
	repo, ctx, l := NewPostgres(db), context.Background(), sampleLeague(t)
	if err := repo.CreateLeague(ctx, l); err != nil {
		t.Fatal(err)
	}
	battleID := league.FixtureBattleID(l.Fixtures[0].ID, 1)
	schedule(t, repo, startFirstFixture(t, db, battleID))
	home, away := l.Entrants[l.Fixtures[0].Home].FactionID, l.Entrants[l.Fixtures[0].Away].FactionID
	if _, err := db.Exec(ctx, `UPDATE battle_runs SET status='completed', completed_at=now(), result=$2 WHERE id=$1`, battleID,
		fmt.Sprintf(`{"winnerFactionId":%q,"reason":"castle_destroyed","ticksRun":90,"factions":[{"factionId":%q,"castleHealth":120,"damageDealt":400},{"factionId":%q,"castleHealth":0,"damageDealt":150}]}`, home, home, away)); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetLeague(ctx, l.ID)
	if err != nil {
		t.Fatal(err)
	}
	outcome := got.Fixtures[0].Battle
	if got.Fixtures[0].Status != league.FixtureRunning || outcome == nil || outcome.Status != "completed" || *outcome.WinnerFactionID != home ||
		outcome.Reason != "castle_destroyed" || outcome.Factions[home].CastleHealth != 120 || outcome.Factions[away].DamageDealt != 150 {
		t.Fatalf("fixture outcome = %+v", outcome)
	}
	semifinals := []league.Fixture{{ID: uuid(t), Sequence: 24, Stage: league.StageSemifinal, Round: 1, Tie: 1, Leg: 1, Home: 0, Away: 3, ScenarioID: "frontier-siege", Status: league.FixturePending}}
	schedule(t, repo, func(ctx context.Context, store leagueservice.SchedulerStore) error {
		for range 2 {
			if err := store.AddFixtures(ctx, l.ID, semifinals); err != nil {
				return err
			}
		}
		return store.CompleteLeague(ctx, l.ID, 3)
	})
	got, err = repo.GetLeague(ctx, l.ID)
	if err != nil || len(got.Fixtures) != 25 || got.Fixtures[24].Group != nil || got.Status != league.StatusCompleted || *got.Champion != 3 || got.CompletedAt == nil {
		t.Fatalf("completed league = %+v, %v", got, err)
	}
	summaries, err := repo.ListLeagues(ctx, 10)
	if err != nil || len(summaries) != 1 || *summaries[0].ChampionName != "Faction 3" || summaries[0].Settled != 0 || summaries[0].GroupCount != 2 {
		t.Fatalf("summaries = %+v, %v", summaries, err)
	}
}

// startFirstFixture is a pass that sees an idle engine, queues battleID, and links it
// to the league's first fixture.
func startFirstFixture(t *testing.T, db *pgxpool.Pool, battleID string) func(context.Context, leagueservice.SchedulerStore) error {
	return func(ctx context.Context, store leagueservice.SchedulerStore) error {
		running, err := store.RunningLeagues(ctx)
		if err != nil || len(running) != 1 || len(running[0].Fixtures) != 24 {
			t.Fatalf("running = %d leagues, %v", len(running), err)
		}
		requireEngine(t, ctx, store, battleID, false)
		insertBattle(t, db, battleID)
		requireEngine(t, ctx, store, battleID, true)
		f := running[0].Fixtures[0]
		f.Status, f.Attempts, f.BattleID = league.FixtureRunning, 1, &battleID
		return store.SaveFixture(ctx, f)
	}
}

// requireEngine checks that the engine is busy, and the battle exists, exactly when queued.
func requireEngine(t *testing.T, ctx context.Context, store leagueservice.SchedulerStore, battleID string, queued bool) {
	t.Helper()
	busy, err := store.EngineBusy(ctx)
	exists, existsErr := store.BattleExists(ctx, battleID)
	if err != nil || existsErr != nil || busy != queued || exists != queued {
		t.Fatalf("busy %v %v, exists %v %v, want %v", busy, err, exists, existsErr, queued)
	}
}

func TestScheduleLockAdmitsOnePassAtATime(t *testing.T) {
	db := privateDatabase(t)
	repo := NewPostgres(db)
	holding, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		_, err := repo.Schedule(context.Background(), func(context.Context, leagueservice.SchedulerStore) error {
			close(holding)
			<-release
			return nil
		})
		finished <- err
	}()
	<-holding
	ran, err := repo.Schedule(context.Background(), func(context.Context, leagueservice.SchedulerStore) error {
		t.Error("second pass ran while the lock was held")
		return nil
	})
	close(release)
	if err != nil || ran {
		t.Fatalf("second Schedule() = %v, %v", ran, err)
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	failure := errors.New("pass failed")
	if ran, err := repo.Schedule(context.Background(), func(context.Context, leagueservice.SchedulerStore) error { return failure }); !ran || !errors.Is(err, failure) {
		t.Fatalf("failing pass = %v, %v", ran, err)
	}
}

func TestCancelLeagueAgainstPostgres(t *testing.T) {
	db := privateDatabase(t)
	repo, ctx, l := NewPostgres(db), context.Background(), sampleLeague(t)
	battleID := uuid(t)
	insertBattle(t, db, battleID)
	l.Fixtures[0].Status, l.Fixtures[0].Attempts, l.Fixtures[0].BattleID = league.FixtureRunning, 1, &battleID
	l.Fixtures[1].Status = league.FixtureCompleted
	if err := repo.CreateLeague(ctx, l); err != nil {
		t.Fatal(err)
	}
	cancelled, running, err := repo.CancelLeague(ctx, l.ID)
	if err != nil || !cancelled || len(running) != 1 || running[0] != battleID {
		t.Fatalf("cancel = %v %v %v", cancelled, running, err)
	}
	got, err := repo.GetLeague(ctx, l.ID)
	if err != nil || got.Status != league.StatusCancelled || got.Fixtures[0].Status != league.FixtureCancelled || got.Fixtures[1].Status != league.FixtureCompleted || got.Fixtures[2].Status != league.FixtureCancelled {
		t.Fatalf("cancelled league = %+v, %v", got, err)
	}
	if cancelled, running, err := repo.CancelLeague(ctx, l.ID); err != nil || cancelled || running != nil {
		t.Fatalf("repeat cancel = %v %v %v", cancelled, running, err)
	}
	if _, _, err := repo.CancelLeague(ctx, uuid(t)); !errors.Is(err, leagueservice.ErrLeagueNotFound) {
		t.Fatalf("missing err = %v", err)
	}
	done := sampleLeague(t)
	if err := repo.CreateLeague(ctx, done); err != nil {
		t.Fatal(err)
	}
	schedule(t, repo, func(ctx context.Context, store leagueservice.SchedulerStore) error {
		return store.CompleteLeague(ctx, done.ID, 0)
	})
	if _, _, err := repo.CancelLeague(ctx, done.ID); !errors.Is(err, leagueservice.ErrLeagueNotActive) {
		t.Fatalf("completed err = %v", err)
	}
}

func TestBattleReadShowsItsLeague(t *testing.T) {
	db := privateDatabase(t)
	ctx, l := context.Background(), sampleLeague(t)
	battleID, outside := uuid(t), uuid(t)
	insertBattle(t, db, battleID)
	insertBattle(t, db, outside)
	l.Fixtures[3].Status, l.Fixtures[3].Attempts, l.Fixtures[3].BattleID = league.FixtureRunning, 1, &battleID
	if err := NewPostgres(db).CreateLeague(ctx, l); err != nil {
		t.Fatal(err)
	}
	battles := battlerepo.NewPostgres(db)
	run, err := battles.GetBattle(ctx, battleID)
	if err != nil || run.League == nil {
		t.Fatalf("league battle = %+v, %v", run, err)
	}
	f := l.Fixtures[3]
	if got := run.League; got.ID != l.ID || got.Name != l.Name || got.FixtureID != f.ID || got.Stage != league.StageGroup || *got.Group != league.GroupLetter(*f.Group) || got.Round != f.Round || got.Leg != f.Leg {
		t.Fatalf("battle league = %+v", got)
	}
	if run, err := battles.GetBattle(ctx, outside); err != nil || run.League != nil {
		t.Fatalf("battle outside a league = %+v, %v", run, err)
	}
}
