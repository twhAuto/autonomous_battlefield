// Checks the battle admission triggers (migrations 00011, 00016 and 00027): per-person
// and global caps across connections, paused battles still counting, separate Player
// and Tester allowances, and admin-exempt battles that skip allowances but count globally.

package migrations_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/postgres"
)

func insertBattle(db *pgxpool.Pool, n int, owner string) error {
	return insertBattleWith(db, n, owner, "")
}

func insertTesterBattle(db *pgxpool.Pool, n int, owner string) error {
	return insertBattleWith(db, n, owner, "tester_allowance")
}

func insertExemptBattle(db *pgxpool.Pool, n int, owner string) error {
	return insertBattleWith(db, n, owner, "admission_exempt")
}

// insertBattleWith inserts a queued battle, setting flag to true when one is named.
func insertBattleWith(db *pgxpool.Pool, n int, owner, flag string) error {
	columns, values := "", ""
	if flag != "" {
		columns, values = ","+flag, ",true"
	}
	_, err := db.Exec(context.Background(), `INSERT INTO battle_runs(id,scenario_id,scenario_digest,scenario_snapshot,requested_ticks,owner_id,initial_state,current_state`+columns+`) VALUES($1,'s','d','{}',10,$2,'{}','{}'`+values+`)`, fmt.Sprintf("00000000-0000-4000-8000-%012d", n), owner)
	return err
}

// limited reports the admission triggers' "limit reached" error.
func limited(err error) bool { var e *pgconn.PgError; return errors.As(err, &e) && e.Code == "P0429" }

func TestBattleAdmissionAcrossConnections(t *testing.T) {
	db := migratedSchema(t)
	// Independent pool connections stand in for separate replicas.
	var wg sync.WaitGroup
	results := make(chan error, 12)
	for i := range 12 {
		wg.Add(1)
		go func(n int) { defer wg.Done(); results <- insertTesterBattle(db, n, "owner") }(i)
	}
	wg.Wait()
	close(results)
	accepted := 0
	for e := range results {
		if e == nil {
			accepted++
		} else if !limited(e) {
			t.Fatal(e)
		}
	}
	if accepted != 2 {
		t.Fatalf("accepted %d; want 2", accepted)
	}
	mustExec(t, db, "UPDATE battle_runs SET paused=true")
	if e := insertTesterBattle(db, 20, "owner"); !limited(e) {
		t.Fatalf("paused evaded limit: %v", e)
	}
	mustExec(t, db, "UPDATE battle_runs SET status='completed'")
	if e := insertTesterBattle(db, 21, "owner"); e != nil {
		t.Fatal(e)
	}
	// Existing ID reaches uniqueness instead of charging a quota twice.
	if e := insertTesterBattle(db, 21, "owner"); e == nil || limited(e) {
		t.Fatalf("idempotent insert: %v", e)
	}
}

// admitted and rejected check one battle start against the admission triggers.
func admitted(t *testing.T, err error, start string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", start, err)
	}
}

func rejected(t *testing.T, err error, start string) {
	t.Helper()
	if !limited(err) {
		t.Fatalf("%s was not limited: %v", start, err)
	}
}

func TestPlayerAndTesterUnfinishedAllowances(t *testing.T) {
	db := migratedSchema(t)
	// One unfinished battle per Player, two per Tester, four across the service.
	admitted(t, insertBattle(db, 1, "player"), "first Player battle")
	rejected(t, insertBattle(db, 2, "player"), "second unfinished Player battle")
	admitted(t, insertTesterBattle(db, 3, "tester"), "first Tester battle")
	admitted(t, insertTesterBattle(db, 4, "tester"), "second Tester battle")
	rejected(t, insertTesterBattle(db, 5, "tester"), "third unfinished Tester battle")
	admitted(t, insertBattle(db, 6, "other"), "fourth battle in the service")
	rejected(t, insertTesterBattle(db, 7, "other-tester"), "fifth unfinished battle in the service")
}

func TestPlayerAndTesterDailyAllowances(t *testing.T) {
	db := migratedSchema(t)
	n := 0
	// start finishes earlier battles so that only the daily allowance applies.
	start := func(owner string, tester bool) error {
		n++
		mustExec(t, db, "UPDATE battle_runs SET status='completed'")
		if tester {
			return insertTesterBattle(db, n, owner)
		}
		return insertBattle(db, n, owner)
	}
	// Five battles per Player and ten per Tester each UTC day.
	for range 5 {
		admitted(t, start("player", false), "Player battle within the daily allowance")
	}
	rejected(t, start("player", false), "sixth Player battle today")
	for range 10 {
		admitted(t, start("tester", true), "Tester battle within the daily allowance")
	}
	rejected(t, start("tester", true), "eleventh Tester battle today")
	// The flag on the new battle, not the owner's history, picks the allowance.
	admitted(t, start("player", true), "Tester allowance for a former Player")
	// Operators can change each allowance, and a new UTC day resets the counts.
	mustExec(t, db, "UPDATE runtime_policy SET tester_daily_battles=11")
	admitted(t, start("tester", true), "battle within a raised Tester allowance")
	mustExec(t, db, "UPDATE battle_runs SET created_at=date_trunc('day',now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'-interval '1 second'")
	admitted(t, start("player", false), "Player battle on a new day")
}

func TestAdminBattlesSkipAllowancesButCountGlobally(t *testing.T) {
	db := migratedSchema(t)
	mustExec(t, db, "UPDATE runtime_policy SET player_daily_battles=1")
	// Exempt rows pass the daily, per-person and global limits.
	for n := range 5 {
		if e := insertExemptBattle(db, n, "admin"); e != nil {
			t.Fatalf("exempt battle %d: %v", n, e)
		}
	}
	// They still occupy the engine, so other identities meet the global cap.
	if e := insertBattle(db, 10, "player"); !limited(e) {
		t.Fatalf("player admitted beside five unfinished exempt battles: %v", e)
	}
	mustExec(t, db, "UPDATE battle_runs SET status='completed'")
	if e := insertBattle(db, 11, "player"); e != nil {
		t.Fatal(e)
	}
	// The flag, not the owner name, grants the exemption.
	if e := insertBattle(db, 12, "admin"); !limited(e) {
		t.Fatalf("non-exempt row evaded the daily limit: %v", e)
	}
}

func TestRoleBattleAllowancesMigration(t *testing.T) {
	ctx := context.Background()
	db, _ := privateSchema(t)
	provider, err := postgres.NewMigrationProvider(db)
	if err != nil {
		t.Fatal(err)
	}
	step := func(_ any, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	const upgraded = `SELECT player_daily_battles,player_unfinished,tester_daily_battles,tester_unfinished FROM runtime_policy WHERE id`
	const previous = `SELECT daily_battles,user_unfinished FROM runtime_policy WHERE id`
	// Allowances at the previous defaults move to the new Player defaults, and Down
	// keeps whatever the row holds.
	step(provider.UpTo(ctx, 26))
	step(provider.UpTo(ctx, 27))
	wantAllowances(t, db, upgraded, 5, 1, 10, 2)
	step(provider.DownTo(ctx, 26))
	wantAllowances(t, db, previous, 5, 1)
	// Operator-set values are kept in both directions, including one equal to a new
	// default.
	step(db.ExecContext(ctx, `UPDATE runtime_policy SET daily_battles=3`))
	step(provider.UpTo(ctx, 27))
	wantAllowances(t, db, upgraded, 3, 1, 10, 2)
	step(provider.DownTo(ctx, 26))
	wantAllowances(t, db, previous, 3, 1)
}

// wantAllowances scans one runtime_policy row and compares its integer columns.
func wantAllowances(t *testing.T, db *sql.DB, query string, want ...int) {
	t.Helper()
	got := make([]int, len(want))
	dest := make([]any, len(want))
	for i := range got {
		dest[i] = &got[i]
	}
	if err := db.QueryRowContext(context.Background(), query).Scan(dest...); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("%s = %v; want %v", query, got, want)
	}
}
