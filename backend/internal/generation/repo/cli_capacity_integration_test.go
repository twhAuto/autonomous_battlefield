// Checks generation claims against the shared CLI capacity: a claimed job holds its slot
// until Finish, battle decisions and generations share the slots and the per-battle
// execution budget, and the AI kill switch stops new work.

package repo

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/generation"
)

// insertBattle queues a minimal battle owned by owner; the admission triggers apply.
func insertBattle(p *Postgres, n int, owner string) error {
	_, err := p.db.Exec(context.Background(), `INSERT INTO battle_runs(id,scenario_id,scenario_digest,scenario_snapshot,requested_ticks,owner_id,initial_state,current_state) VALUES($1,'s','d','{}',10,$2,'{}','{}')`, fmt.Sprintf("00000000-0000-4000-8000-%012d", n), owner)
	return err
}

func TestGlobalCLISlotHeldUntilWorkerCompletion(t *testing.T) {
	p := database(t)
	sqlExec(t, p, "UPDATE runtime_policy SET cli_concurrency=1")
	ctx := context.Background()
	c := generation.DefaultConfig()
	first := admit(t, p, "one", "a", c)
	second := admit(t, p, "two", "a", c)
	job, e := p.Claim(ctx, "vm-one", []string{c.ProfileID})
	if e != nil || job.ID != first.ID {
		t.Fatal(job, e)
	}
	if _, e = p.Claim(ctx, "vm-two", []string{c.ProfileID}); !errors.Is(e, generation.ErrNotFound) {
		t.Fatalf("second CLI started: %v", e)
	}
	if ok, e := p.Submit(ctx, first.ID, "A valid background"); e != nil || !ok {
		t.Fatal(ok, e)
	}
	if _, e = p.Claim(ctx, "vm-two", []string{c.ProfileID}); !errors.Is(e, generation.ErrNotFound) {
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
	if _, _, e = p.Admit(ctx, "third", "a", draft, c, writer); !errors.Is(e, generation.ErrLimited) {
		t.Fatalf("kill switch admitted work: %v", e)
	}
}

func TestDecisionExecutionBudgetAndSharedGenerationSlot(t *testing.T) {
	p := database(t)
	sqlExec(t, p, "UPDATE runtime_policy SET cli_concurrency=1")
	ctx := context.Background()
	if e := insertBattle(p, 1, "admin-identity"); e != nil {
		t.Fatal(e)
	}
	sqlExec(t, p, "UPDATE runtime_policy SET decision_executions=1")
	const battleID = "00000000-0000-4000-8000-000000000001"
	for n := 1; n <= 2; n++ {
		sqlExec(t, p, `INSERT INTO battle_decisions(id,battle_id,attempt,faction_id,target_tick,status,controller_type,profile_id,runtime,prompt_version,tool_schema_version,observation,observation_hash) VALUES($1,$2,1,$1,1,'pending','model','cli','cli','v','v','{}','h')`, fmt.Sprintf("10000000-0000-4000-8000-%012d", n), battleID)
	}
	claim := func(id string) string {
		var status string
		e := p.db.QueryRow(ctx, `UPDATE battle_decisions SET status='running',lease_owner='vm',lease_expires_at=now()+interval '1 minute' WHERE id=$1 RETURNING status`, id).Scan(&status)
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
	c := generation.DefaultConfig()
	admit(t, p, "friend", "a", c)
	if _, e := p.Claim(ctx, "vm-two", []string{c.ProfileID}); !errors.Is(e, generation.ErrNotFound) {
		t.Fatalf("generation overlapped decision: %v", e)
	}
	sqlExec(t, p, "UPDATE battle_decisions SET status='failed',usage='{}',lease_owner=NULL,lease_expires_at=NULL WHERE id=$1", first)
	if s := claim(second); s != "failed" {
		t.Fatalf("budget exceeded: %s", s)
	}
	var used int
	if e := p.db.QueryRow(ctx, "SELECT ai_executions FROM battle_runs WHERE id=$1", battleID).Scan(&used); e != nil || used != 1 {
		t.Fatal(used, e)
	}
	sqlExec(t, p, "UPDATE runtime_policy SET ai_enabled=false")
	if _, e := p.Claim(ctx, "vm", []string{c.ProfileID}); !errors.Is(e, generation.ErrNotFound) {
		t.Fatalf("disabled worker claim: %v", e)
	}
}

func TestFourSharedCLISlotsAcrossConcurrentClaims(t *testing.T) {
	p := database(t)
	ctx := context.Background()
	c := generation.DefaultConfig()
	if err := insertBattle(p, 1, "legacy-admin"); err != nil {
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
		} else if !errors.Is(err, generation.ErrNotFound) {
			t.Fatal(err)
		}
	}
	if claimed != 3 {
		t.Fatalf("generation claimed %d slots alongside one battle; want 3", claimed)
	}
	sqlExec(t, p, `UPDATE battle_decisions SET status='accepted' WHERE id=$1`, decision)
	if _, err := p.Claim(ctx, "vm", []string{c.ProfileID}); !errors.Is(err, generation.ErrNotFound) {
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
	if _, err := p.Claim(ctx, "vm", []string{c.ProfileID}); !errors.Is(err, generation.ErrNotFound) {
		t.Fatalf("disabled AI admitted work: %v", err)
	}
}
