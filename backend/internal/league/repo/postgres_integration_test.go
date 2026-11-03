// Exercises league storage against PostgreSQL in a private schema, so the engine-idle
// check sees only this test's battles: creation and reads, fixture outcomes joined from
// battles with their rules versions, the scheduler lock, cancellation, disqualification,
// and the league shown on a battle read.

package repo

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
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
	l := league.League{ID: uuid(t), OwnerID: "admin", Name: "Autumn cup", Status: league.StatusRunning, RequestDigest: "digest", BattleSalt: strings.Repeat("ab", 16), GroupCount: 2,
		Settings: league.Settings{ScenarioIDs: []string{"frontier-siege", "castle-siege"}, TickCount: 500, DecisionIntervalTicks: &interval}}
	for i := range 8 {
		controller := game.ControllerConfig{Type: "model", ProfileID: "claude-subscription-local", ModelIdentifier: "sonnet", ReasoningEffort: "low"}
		l.Entrants = append(l.Entrants, league.Entrant{Index: i, Group: i % 2, FactionID: uuid(t), FactionName: fmt.Sprintf("Faction %d", i), Controller: controller})
	}
	l.Fixtures = l.OpeningFixtures()
	return l
}

func insertBattle(t *testing.T, db *pgxpool.Pool, id string, factionIDs ...string) {
	t.Helper()
	factions := []map[string]string{}
	for _, factionID := range factionIDs {
		factions = append(factions, map[string]string{"id": factionID})
	}
	snapshot, _ := json.Marshal(map[string]any{"factions": factions, "rulesVersion": "13"})
	if _, err := db.Exec(context.Background(), `INSERT INTO battle_runs(id,scenario_id,scenario_digest,scenario_snapshot,requested_ticks,owner_id,initial_state,current_state,admission_exempt,decision_budget_exempt)
		VALUES($1,'frontier-siege','d',$2,500,'admin','{}','{}',true,true)`, id, snapshot); err != nil {
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
	if got.Name != l.Name || got.Status != league.StatusRunning || got.RequestDigest != "digest" || got.BattleSalt != l.BattleSalt || got.GroupCount != 2 || got.Settings.TickCount != 500 ||
		*got.Settings.DecisionIntervalTicks != 10 || got.Settings.TickDuration != nil || len(got.Settings.ScenarioIDs) != 2 || got.Champion != nil || got.CompletedAt != nil {
		t.Fatalf("league = %+v", got)
	}
	if len(got.Entrants) != 8 || got.Entrants[5] != l.Entrants[5] {
		t.Fatalf("entrant = %+v, want %+v", got.Entrants[5], l.Entrants[5])
	}
	if len(got.Fixtures) != 4 || got.Fixtures[3].ID != l.Fixtures[3].ID || *got.Fixtures[3].Group != *l.Fixtures[3].Group || got.Fixtures[3].Tie != 2 ||
		got.Fixtures[3].Home != l.Fixtures[3].Home || got.Fixtures[3].Battle != nil {
		t.Fatalf("fixture = %+v", got.Fixtures[3])
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
	battleID := league.FixtureBattleID(l.BattleSalt, l.Fixtures[0].ID, 1)
	schedule(t, repo, startFirstFixture(t, db, l, battleID))
	home, away := l.Entrants[l.Fixtures[0].Home].FactionID, l.Entrants[l.Fixtures[0].Away].FactionID
	if _, err := db.Exec(ctx, `UPDATE battle_runs SET status='completed', completed_at=now(), current_tick=90, result=$2 WHERE id=$1`, battleID,
		fmt.Sprintf(`{"winnerFactionId":%q,"reason":"castle_destroyed","ticksRun":90,"factions":[{"factionId":%q,"castleHealth":120,"damageDealt":400},{"factionId":%q,"castleHealth":0,"damageDealt":150}]}`, home, home, away)); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetLeague(ctx, l.ID)
	if err != nil {
		t.Fatal(err)
	}
	outcome := got.Fixtures[0].Battle
	if got.Fixtures[0].Status != league.FixtureRunning || outcome == nil || outcome.Status != "completed" || outcome.CurrentTick != 90 || outcome.RequestedTicks != 500 || *outcome.WinnerFactionID != home ||
		outcome.Reason != "castle_destroyed" || outcome.Factions[home].CastleHealth != 120 || outcome.Factions[away].DamageDealt != 150 {
		t.Fatalf("fixture outcome = %+v", outcome)
	}
	semifinals := []league.Fixture{{ID: uuid(t), Sequence: 4, Stage: league.StageSemifinal, Round: 1, Tie: 1, Home: 0, Away: 3, ScenarioID: "frontier-siege", Status: league.FixturePending}}
	schedule(t, repo, func(ctx context.Context, store leagueservice.SchedulerStore) error {
		for range 2 {
			if err := store.AddFixtures(ctx, l.ID, semifinals); err != nil {
				return err
			}
		}
		return store.CompleteLeague(ctx, l.ID, 3)
	})
	got, err = repo.GetLeague(ctx, l.ID)
	if err != nil || len(got.Fixtures) != 5 || got.Fixtures[4].Group != nil || got.Status != league.StatusCompleted || *got.Champion != 3 || got.CompletedAt == nil {
		t.Fatalf("completed league = %+v, %v", got, err)
	}
	summaries, err := repo.ListLeagues(ctx, leagueservice.ListQuery{Limit: 10})
	if err != nil || len(summaries) != 1 || *summaries[0].ChampionName != "Faction 3" || summaries[0].Settled != 0 || summaries[0].GroupCount != 2 {
		t.Fatalf("summaries = %+v, %v", summaries, err)
	}
}

// startFirstFixture is a pass that sees an idle engine, queues battleID, and links it
// to the league's first fixture.
func startFirstFixture(t *testing.T, db *pgxpool.Pool, l league.League, battleID string) func(context.Context, leagueservice.SchedulerStore) error {
	return func(ctx context.Context, store leagueservice.SchedulerStore) error {
		running, err := store.RunningLeagues(ctx)
		if err != nil || len(running) != 1 || len(running[0].Fixtures) != 4 || running[0].BattleSalt != l.BattleSalt {
			t.Fatalf("running = %d leagues, %v", len(running), err)
		}
		requireEngine(t, ctx, store, battleID, false)
		home, away := l.Entrants[l.Fixtures[0].Home].FactionID, l.Entrants[l.Fixtures[0].Away].FactionID
		insertBattle(t, db, battleID, home, away)
		requireEngine(t, ctx, store, battleID, true)
		origin, err := store.Battle(ctx, battleID)
		if err != nil || origin.OwnerID != "admin" || !origin.DecisionBudgetExempt || origin.ScenarioID != "frontier-siege" || fmt.Sprint(origin.FactionIDs) != fmt.Sprint([]string{home, away}) {
			t.Fatalf("battle origin = %+v, %v", origin, err)
		}
		f := running[0].Fixtures[0]
		f.Status, f.Attempts, f.BattleID = league.FixtureRunning, 1, &battleID
		return store.SaveFixture(ctx, f)
	}
}

// requireEngine checks that one battle is unfinished, and the battle exists, exactly when
// queued.
func requireEngine(t *testing.T, ctx context.Context, store leagueservice.SchedulerStore, battleID string, queued bool) {
	t.Helper()
	capacity, err := store.Capacity(ctx)
	origin, originErr := store.Battle(ctx, battleID)
	if err != nil || originErr != nil || (capacity.Unfinished == 1) != queued || (origin != nil) != queued {
		t.Fatalf("capacity %+v %v, battle %+v %v, want %v", capacity, err, origin, originErr, queued)
	}
}

func TestCapacityCountsUnfinishedBattlesAndTheirCLISides(t *testing.T) {
	db := privateDatabase(t)
	repo := NewPostgres(db)
	ctx := context.Background()
	battle := func(status string, controllers ...map[string]string) {
		factions := []map[string]any{}
		for i, controller := range controllers {
			factions = append(factions, map[string]any{"id": fmt.Sprintf("faction-%d", i), "controller": controller})
		}
		snapshot, _ := json.Marshal(map[string]any{"factions": factions, "rulesVersion": "13"})
		if _, err := db.Exec(ctx, `INSERT INTO battle_runs(id,scenario_id,scenario_digest,scenario_snapshot,requested_ticks,owner_id,initial_state,current_state,status,admission_exempt)
			VALUES($1,'frontier-siege','d',$2,500,'admin','{}','{}',$3,true)`, uuid(t), snapshot, status); err != nil {
			t.Fatal(err)
		}
	}
	cli := map[string]string{"type": "model", "runtime": "cli"}
	api := map[string]string{"type": "model", "runtime": "api"}
	scripted := map[string]string{"type": "scripted"}
	battle("queued", cli, cli)
	battle("running", cli, scripted)
	battle("running", api, scripted)
	battle("completed", cli, cli)
	if _, err := db.Exec(ctx, `UPDATE runtime_policy SET cli_concurrency = 3`); err != nil {
		t.Fatal(err)
	}
	schedule(t, repo, func(ctx context.Context, store leagueservice.SchedulerStore) error {
		capacity, err := store.Capacity(ctx)
		if err != nil || capacity != (leagueservice.Capacity{Unfinished: 3, CLISides: 3, CLISlots: 3}) {
			t.Fatalf("capacity = %+v, %v", capacity, err)
		}
		return nil
	})
	if slots, err := repo.CLISlots(ctx); err != nil || slots != 3 {
		t.Fatalf("CLI slots = %d, %v", slots, err)
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
	requireStranded(t, repo, leagueservice.StrandedBattle{LeagueID: l.ID, BattleID: battleID, OwnerID: "admin"})
	got, err := repo.GetLeague(ctx, l.ID)
	if err != nil || got.Status != league.StatusCancelled || got.Fixtures[0].Status != league.FixtureCancelled || got.Fixtures[1].Status != league.FixtureCompleted || got.Fixtures[2].Status != league.FixtureCancelled {
		t.Fatalf("cancelled league = %+v, %v", got, err)
	}
	if cancelled, running, err := repo.CancelLeague(ctx, l.ID); err != nil || cancelled || len(running) != 1 {
		t.Fatalf("repeat cancel while the battle runs = %v %v %v", cancelled, running, err)
	}
	if _, err := db.Exec(ctx, `UPDATE battle_runs SET status='stopped', completed_at=now() WHERE id=$1`, battleID); err != nil {
		t.Fatal(err)
	}
	if cancelled, running, err := repo.CancelLeague(ctx, l.ID); err != nil || cancelled || len(running) != 0 {
		t.Fatalf("repeat cancel after the stop = %v %v %v", cancelled, running, err)
	}
	requireStranded(t, repo)
}

// requireStranded checks the battles a scheduling pass would stop.
func requireStranded(t *testing.T, repo *Postgres, want ...leagueservice.StrandedBattle) {
	t.Helper()
	schedule(t, repo, func(ctx context.Context, store leagueservice.SchedulerStore) error {
		stranded, err := store.StrandedBattles(ctx)
		if err != nil || fmt.Sprint(stranded) != fmt.Sprint(append([]leagueservice.StrandedBattle{}, want...)) {
			t.Fatalf("stranded = %+v, %v, want %+v", stranded, err, want)
		}
		return nil
	})
}

func TestDisqualificationAgainstPostgres(t *testing.T) {
	db := privateDatabase(t)
	repo, ctx, l := NewPostgres(db), context.Background(), sampleLeague(t)
	battleID := uuid(t)
	insertBattle(t, db, battleID)
	l.Fixtures[0].Status, l.Fixtures[0].Attempts, l.Fixtures[0].BattleID = league.FixtureRunning, 1, &battleID
	if err := repo.CreateLeague(ctx, l); err != nil {
		t.Fatal(err)
	}
	entrant := l.Fixtures[0].Home
	if err := repo.Exclusive(ctx, disqualifyFirstHome(t, repo, l.ID)); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetLeague(ctx, l.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored := got.Entrants[entrant].Disqualified
	if stored == nil || stored.By != "admin" || stored.Reason != "too slow" || time.Since(stored.At) > time.Minute || got.Entrants[l.Fixtures[0].Away].Disqualified != nil {
		t.Fatalf("entrants = %+v", got.Entrants)
	}
	opening, walkover := got.Fixtures[0], got.Fixtures[5]
	if opening.Status != league.FixtureForfeited || opening.Forfeit != league.ForfeitHome || *opening.BattleID != battleID || walkover.Status != league.FixtureForfeited ||
		walkover.Forfeit == "" || walkover.BattleID != nil || got.Fixtures[4].Forfeit != "" {
		t.Fatalf("fixtures = %+v", got.Fixtures)
	}
	if summaries, err := repo.ListLeagues(ctx, leagueservice.ListQuery{Limit: 5}); err != nil || summaries[0].Settled != 3 || !slices.Equal(summaries[0].RulesVersions, []string{"13"}) {
		t.Fatalf("summaries = %+v, %v", summaries, err)
	}
	requireRulesVersionFilter(t, repo, got)
	requireStranded(t, repo, leagueservice.StrandedBattle{LeagueID: l.ID, BattleID: battleID, OwnerID: "admin"})
	if _, err := db.Exec(ctx, `UPDATE battle_runs SET status='stopped', completed_at=now() WHERE id=$1`, battleID); err != nil {
		t.Fatal(err)
	}
	requireStranded(t, repo)
}

// requireRulesVersionFilter checks that a league with one battle on rules version 13
// reports that version and is listed only under it.
func requireRulesVersionFilter(t *testing.T, repo *Postgres, got *league.League) {
	t.Helper()
	if versions := got.RulesVersions(); !slices.Equal(versions, []string{"13"}) {
		t.Fatalf("league rules versions = %v", versions)
	}
	for version, want := range map[string]int{"13": 1, "12": 0} {
		summaries, err := repo.ListLeagues(context.Background(), leagueservice.ListQuery{Limit: 5, RulesVersion: version})
		if err != nil || len(summaries) != want {
			t.Fatalf("version %s summaries = %+v, %v", version, summaries, err)
		}
	}
}

// disqualifyFirstHome disqualifies the first fixture's home entrant while no scheduling
// pass can run; a second write by someone else keeps the first disqualification. Group
// A's other opening is void, so its losers' match opens as a walkover.
func disqualifyFirstHome(t *testing.T, repo *Postgres, leagueID string) func(context.Context, leagueservice.SchedulerStore) error {
	return func(ctx context.Context, store leagueservice.SchedulerStore) error {
		requireLocked(t, ctx, repo)
		stored, err := store.League(ctx, leagueID)
		if err != nil {
			return err
		}
		entrant := stored.Fixtures[0].Home
		forfeited, err := stored.Disqualify(entrant, league.Disqualification{By: "admin", Reason: "too slow"})
		if err != nil || len(forfeited) != 1 || forfeited[0].Battle == nil || forfeited[0].Battle.Status != "queued" {
			t.Fatalf("forfeited %+v, %v", forfeited, err)
		}
		stored.Fixtures[1].Status = league.FixtureVoid
		next, _ := stored.Advance()
		return errors.Join(
			store.Disqualify(ctx, leagueID, entrant, league.Disqualification{By: "admin", Reason: "too slow"}),
			store.Disqualify(ctx, leagueID, entrant, league.Disqualification{By: "someone else", Reason: "again"}),
			store.SaveFixture(ctx, forfeited[0]), store.SaveFixture(ctx, stored.Fixtures[1]), store.AddFixtures(ctx, leagueID, next))
	}
}

// requireLocked checks that a scheduling pass cannot run now.
func requireLocked(t *testing.T, ctx context.Context, repo *Postgres) {
	t.Helper()
	if ran, err := repo.Schedule(ctx, func(context.Context, leagueservice.SchedulerStore) error { return nil }); err != nil || ran {
		t.Fatalf("a pass ran during a disqualification: %v, %v", ran, err)
	}
}

func TestCancelLeagueRejectsMissingAndCompletedLeagues(t *testing.T) {
	db := privateDatabase(t)
	repo, ctx := NewPostgres(db), context.Background()
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
	if got := run.League; got.ID != l.ID || got.Name != l.Name || got.FixtureID != f.ID || got.Stage != league.StageGroup || *got.Group != league.GroupLetter(*f.Group) || got.Round != f.Round || got.Tie != f.Tie {
		t.Fatalf("battle league = %+v", got)
	}
	if run, err := battles.GetBattle(ctx, outside); err != nil || run.League != nil {
		t.Fatalf("battle outside a league = %+v, %v", run, err)
	}
}
