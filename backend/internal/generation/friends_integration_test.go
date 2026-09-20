package generation

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
)

func insertFriendBattle(p *Postgres, n int, owner string) error {
	_, err := p.DB.Exec(context.Background(), `INSERT INTO battle_runs(id,scenario_id,scenario_digest,scenario_snapshot,requested_ticks,owner_id,initial_state,current_state) VALUES($1,'s','d','{}',10,$2,'{}','{}')`, fmt.Sprintf("00000000-0000-4000-8000-%012d", n), owner)
	return err
}
func limited(err error) bool { var e *pgconn.PgError; return errors.As(err, &e) && e.Code == "P0429" }

func TestFriendsBattleAdmissionAcrossConnections(t *testing.T) {
	p := database(t)
	// Independent pool connections stand in for separate replicas.
	var wg sync.WaitGroup
	results := make(chan error, 12)
	for i := range 12 {
		wg.Add(1)
		go func(n int) { defer wg.Done(); results <- insertFriendBattle(p, n, "owner") }(i)
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
	sqlExec(t, p, "UPDATE battle_runs SET paused=true")
	if e := insertFriendBattle(p, 20, "owner"); !limited(e) {
		t.Fatalf("paused evaded limit: %v", e)
	}
	if e := insertFriendBattle(p, 21, "other"); e != nil {
		t.Fatal(e)
	}
	if e := insertFriendBattle(p, 22, "other"); e != nil {
		t.Fatal(e)
	}
	if e := insertFriendBattle(p, 23, "third"); !limited(e) {
		t.Fatalf("global limit: %v", e)
	}
	sqlExec(t, p, "UPDATE battle_runs SET status='completed'")
	if e := insertFriendBattle(p, 24, "owner"); e != nil {
		t.Fatal(e)
	}
	// A lower operator allowance is durable and applies to any identity, including Admin.
	sqlExec(t, p, "UPDATE runtime_policy SET daily_battles=3")
	if e := insertFriendBattle(p, 25, "owner"); !limited(e) {
		t.Fatalf("daily limit: %v", e)
	}
	sqlExec(t, p, "UPDATE battle_runs SET created_at=date_trunc('day',now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'-interval '1 second'")
	if e := insertFriendBattle(p, 26, "owner"); e != nil {
		t.Fatal(e)
	}
	// Existing ID reaches uniqueness instead of charging a quota twice.
	if e := insertFriendBattle(p, 26, "owner"); e == nil || limited(e) {
		t.Fatalf("idempotent insert: %v", e)
	}
}

func TestGlobalCLISlotHeldUntilWorkerCompletion(t *testing.T) {
	p := database(t)
	sqlExec(t, p, "UPDATE runtime_policy SET cli_concurrency=1")
	ctx := context.Background()
	c := DefaultConfig()
	first := admit(t, p, "one", "a", c)
	second := admit(t, p, "two", "a", c)
	job, e := p.Claim(ctx, "vm-one", []string{c.ProfileID})
	if e != nil || job.ID != first.ID {
		t.Fatal(job, e)
	}
	if _, e = p.Claim(ctx, "vm-two", []string{c.ProfileID}); !errors.Is(e, ErrNotFound) {
		t.Fatalf("second CLI started: %v", e)
	}
	if ok, e := p.Submit(ctx, first.ID, "A valid background"); e != nil || !ok {
		t.Fatal(ok, e)
	}
	if _, e = p.Claim(ctx, "vm-two", []string{c.ProfileID}); !errors.Is(e, ErrNotFound) {
		t.Fatalf("submission released slot too early: %v", e)
	}
	if ok, e := p.Finish(ctx, first.ID, "", ai.Usage{}, nil); e != nil || !ok {
		t.Fatal(ok, e)
	}
	job, e = p.Claim(ctx, "vm-two", []string{c.ProfileID})
	if e != nil || job.ID != second.ID {
		t.Fatal(job, e)
	}
	sqlExec(t, p, "UPDATE runtime_policy SET ai_enabled=false")
	if _, _, e = p.Admit(ctx, "third", "a", draft, c, "Writer"); !errors.Is(e, ErrLimited) {
		t.Fatalf("kill switch admitted work: %v", e)
	}
}

func TestDecisionExecutionBudgetAndSharedGenerationSlot(t *testing.T) {
	p := database(t)
	sqlExec(t, p, "UPDATE runtime_policy SET cli_concurrency=1")
	ctx := context.Background()
	if e := insertFriendBattle(p, 1, "admin-identity"); e != nil {
		t.Fatal(e)
	}
	sqlExec(t, p, "UPDATE runtime_policy SET decision_executions=1")
	const battleID = "00000000-0000-4000-8000-000000000001"
	for n := 1; n <= 2; n++ {
		sqlExec(t, p, `INSERT INTO battle_decisions(id,battle_id,attempt,faction_id,target_tick,status,controller_type,profile_id,runtime,prompt_version,tool_schema_version,observation,observation_hash) VALUES($1,$2,1,$1,1,'pending','model','cli','cli','v','v','{}','h')`, fmt.Sprintf("10000000-0000-4000-8000-%012d", n), battleID)
	}
	claim := func(id string) string {
		var status string
		e := p.DB.QueryRow(ctx, `UPDATE battle_decisions SET status='running',lease_owner='vm',lease_expires_at=now()+interval '1 minute' WHERE id=$1 RETURNING status`, id).Scan(&status)
		if e != nil {
			t.Fatal(e)
		}
		return status
	}
	first := "10000000-0000-4000-8000-000000000001"
	second := "10000000-0000-4000-8000-000000000002"
	if s := claim(first); s != "running" {
		t.Fatal(s)
	}
	c := DefaultConfig()
	admit(t, p, "friend", "a", c)
	if _, e := p.Claim(ctx, "vm-two", []string{c.ProfileID}); !errors.Is(e, ErrNotFound) {
		t.Fatalf("generation overlapped decision: %v", e)
	}
	sqlExec(t, p, "UPDATE battle_decisions SET status='failed',usage='{}',lease_owner=NULL,lease_expires_at=NULL WHERE id=$1", first)
	if s := claim(second); s != "failed" {
		t.Fatalf("budget exceeded: %s", s)
	}
	var used int
	if e := p.DB.QueryRow(ctx, "SELECT ai_executions FROM battle_runs WHERE id=$1", battleID).Scan(&used); e != nil || used != 1 {
		t.Fatal(used, e)
	}
	sqlExec(t, p, "UPDATE runtime_policy SET ai_enabled=false")
	if _, e := p.Claim(ctx, "vm", []string{c.ProfileID}); !errors.Is(e, ErrNotFound) {
		t.Fatalf("disabled worker claim: %v", e)
	}
}

func TestFourSharedCLISlotsAcrossConcurrentClaims(t *testing.T) {
	p := database(t)
	ctx := context.Background()
	c := DefaultConfig()
	if err := insertFriendBattle(p, 1, "legacy-admin"); err != nil {
		t.Fatal(err)
	}
	const decision = "10000000-0000-4000-8000-000000000001"
	sqlExec(t, p, `INSERT INTO battle_decisions(id,battle_id,attempt,faction_id,target_tick,status,controller_type,profile_id,runtime,prompt_version,tool_schema_version,observation,observation_hash) VALUES($1,'00000000-0000-4000-8000-000000000001',1,$1,1,'pending','model','cli','cli','v','v','{}','h')`, decision)
	sqlExec(t, p, `UPDATE battle_decisions SET status='running',lease_owner='vm',lease_expires_at=now()+interval '1 minute' WHERE id=$1`, decision)
	for i := range 6 {
		admit(t, p, fmt.Sprintf("actor-%d", i), "a", c)
	}
	var wg sync.WaitGroup
	results := make(chan error, 6)
	for i := range 6 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, err := p.Claim(ctx, fmt.Sprintf("vm-%d", n), []string{c.ProfileID})
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)
	claimed := 0
	for err := range results {
		if err == nil {
			claimed++
		} else if !errors.Is(err, ErrNotFound) {
			t.Fatal(err)
		}
	}
	if claimed != 3 {
		t.Fatalf("generation claimed %d slots alongside one battle; want 3", claimed)
	}
	sqlExec(t, p, `UPDATE battle_decisions SET status='accepted' WHERE id=$1`, decision)
	if _, err := p.Claim(ctx, "vm", []string{c.ProfileID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("accepted plan prematurely released capacity: %v", err)
	}
	sqlExec(t, p, `UPDATE battle_decisions SET usage='{}' WHERE id=$1`, decision)
	if _, err := p.Claim(ctx, "vm", []string{c.ProfileID}); err != nil {
		t.Fatalf("completion did not release capacity: %v", err)
	}
	sqlExec(t, p, `UPDATE agent_execution_leases SET expires_at=now()-interval '1 second'`)
	if _, err := p.Claim(ctx, "vm", []string{c.ProfileID}); err != nil {
		t.Fatalf("expired leases blocked capacity: %v", err)
	}
	sqlExec(t, p, `UPDATE runtime_policy SET ai_enabled=false`)
	if _, err := p.Claim(ctx, "vm", []string{c.ProfileID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled AI admitted work: %v", err)
	}
}
