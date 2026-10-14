// Checks the battle admission triggers (migrations 00011 and 00016): per-player and
// global caps across connections, paused battles still counting, the operator's daily
// allowance, and admin-exempt battles that skip allowances but count globally.

package migrations_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func insertBattle(db *pgxpool.Pool, n int, owner string) error {
	_, err := db.Exec(context.Background(), `INSERT INTO battle_runs(id,scenario_id,scenario_digest,scenario_snapshot,requested_ticks,owner_id,initial_state,current_state) VALUES($1,'s','d','{}',10,$2,'{}','{}')`, fmt.Sprintf("00000000-0000-4000-8000-%012d", n), owner)
	return err
}

func insertExemptBattle(db *pgxpool.Pool, n int, owner string) error {
	_, err := db.Exec(context.Background(), `INSERT INTO battle_runs(id,scenario_id,scenario_digest,scenario_snapshot,requested_ticks,owner_id,initial_state,current_state,admission_exempt) VALUES($1,'s','d','{}',10,$2,'{}','{}',true)`, fmt.Sprintf("00000000-0000-4000-8000-%012d", n), owner)
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
		go func(n int) { defer wg.Done(); results <- insertBattle(db, n, "owner") }(i)
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
	if e := insertBattle(db, 20, "owner"); !limited(e) {
		t.Fatalf("paused evaded limit: %v", e)
	}
	if e := insertBattle(db, 21, "other"); e != nil {
		t.Fatal(e)
	}
	if e := insertBattle(db, 22, "other"); e != nil {
		t.Fatal(e)
	}
	if e := insertBattle(db, 23, "third"); !limited(e) {
		t.Fatalf("global limit: %v", e)
	}
	mustExec(t, db, "UPDATE battle_runs SET status='completed'")
	if e := insertBattle(db, 24, "owner"); e != nil {
		t.Fatal(e)
	}
	// A lower operator allowance is durable and applies to every non-exempt identity.
	mustExec(t, db, "UPDATE runtime_policy SET daily_battles=3")
	if e := insertBattle(db, 25, "owner"); !limited(e) {
		t.Fatalf("daily limit: %v", e)
	}
	mustExec(t, db, "UPDATE battle_runs SET created_at=date_trunc('day',now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'-interval '1 second'")
	if e := insertBattle(db, 26, "owner"); e != nil {
		t.Fatal(e)
	}
	// Existing ID reaches uniqueness instead of charging a quota twice.
	if e := insertBattle(db, 26, "owner"); e == nil || limited(e) {
		t.Fatalf("idempotent insert: %v", e)
	}
}

func TestAdminBattlesSkipAllowancesButCountGlobally(t *testing.T) {
	db := migratedSchema(t)
	mustExec(t, db, "UPDATE runtime_policy SET daily_battles=1")
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
