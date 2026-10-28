// Checks migration 00022: a league battle's model decisions continue past the per-battle
// budget while still being counted, other battles' decisions stop at it, the AI gateway
// role can read the exemption, and the downgrade refuses to strand a running league.

package migrations_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twh09/autonomous_battlefield/backend/internal/postgres"
)

func insertBudgetBattle(t *testing.T, db *pgxpool.Pool, exempt bool, executions int) string {
	t.Helper()
	id := randomUUID(t)
	mustExec(t, db, `INSERT INTO battle_runs(id,scenario_id,scenario_digest,scenario_snapshot,requested_ticks,owner_id,initial_state,current_state,admission_exempt,decision_budget_exempt,ai_executions)
		VALUES($1,'s','d','{}',10,'admin','{}','{}',true,$2,$3)`, id, exempt, executions)
	return id
}

// insertModelDecision returns the status and error category the gate trigger leaves.
func insertModelDecision(t *testing.T, db *pgxpool.Pool, battleID string) (string, string, string) {
	t.Helper()
	id := randomUUID(t)
	var status, category string
	err := db.QueryRow(context.Background(), `INSERT INTO battle_decisions(id,battle_id,attempt,faction_id,target_tick,status,controller_type,profile_id,runtime,prompt_version,tool_schema_version,observation,observation_hash)
		VALUES($1,$2,1,$3,1,'pending','model','profile','api','v','v','{}','hash') RETURNING status,error_category`, id, battleID, randomUUID(t)).Scan(&status, &category)
	if err != nil {
		t.Fatal(err)
	}
	return id, status, category
}

// claim leases a decision and returns the status and category admission leaves.
func claim(t *testing.T, db *pgxpool.Pool, decisionID string) (string, string) {
	t.Helper()
	var status, category string
	if err := db.QueryRow(context.Background(), `UPDATE battle_decisions SET status='running',lease_owner='worker',lease_expires_at=now()+interval '1 minute'
		WHERE id=$1 RETURNING status,error_category`, decisionID).Scan(&status, &category); err != nil {
		t.Fatal(err)
	}
	return status, category
}

func TestLeagueBattlesSkipTheDecisionBudget(t *testing.T) {
	db := migratedSchema(t)
	mustExec(t, db, "UPDATE runtime_policy SET decision_executions=1")

	spent := insertBudgetBattle(t, db, false, 1)
	if _, status, category := insertModelDecision(t, db, spent); status != "failed" || category != "ai_disabled_or_budget" {
		t.Fatalf("spent budget: %s %s", status, category)
	}
	exempt := insertBudgetBattle(t, db, true, 1)
	decision, status, _ := insertModelDecision(t, db, exempt)
	if status != "pending" {
		t.Fatalf("exempt battle window %s", status)
	}
	if status, _ := claim(t, db, decision); status != "running" {
		t.Fatalf("exempt claim %s", status)
	}
	var executions int
	if err := db.QueryRow(context.Background(), `SELECT ai_executions FROM battle_runs WHERE id=$1`, exempt).Scan(&executions); err != nil || executions != 2 {
		t.Fatalf("exempt executions = %d, %v; want counted", executions, err)
	}

	racing := insertBudgetBattle(t, db, false, 0)
	decision, _, _ = insertModelDecision(t, db, racing)
	mustExec(t, db, `UPDATE battle_runs SET ai_executions=1 WHERE id=$1`, racing)
	if status, category := claim(t, db, decision); status != "failed" || category != "decision_budget" {
		t.Fatalf("claim past budget: %s %s", status, category)
	}

	var readable bool
	if err := db.QueryRow(context.Background(), `SELECT has_column_privilege('battlefield_ai_gateway', 'battle_runs', 'decision_budget_exempt', 'SELECT')`).Scan(&readable); err != nil || !readable {
		t.Fatalf("gateway cannot read the exemption: %v %v", readable, err)
	}
}

func TestLeagueDowngradeRefusesRunningLeagues(t *testing.T) {
	db, _ := privateSchema(t)
	ctx := context.Background()
	provider, err := postgres.NewMigrationProvider(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 22); err != nil {
		t.Fatal(err)
	}
	league := randomUUID(t)
	if _, err = db.ExecContext(ctx, `INSERT INTO leagues(id,owner_id,name,request_digest,scenario_ids,requested_ticks,group_count) VALUES($1,'admin','Cup','d','{frontier-siege}',500,2)`, league); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Down(ctx); err == nil {
		t.Fatal("downgrade dropped a running league")
	}
	if _, err = db.ExecContext(ctx, `UPDATE leagues SET status='cancelled', completed_at=now() WHERE id=$1`, league); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Down(ctx); err != nil {
		t.Fatalf("downgrade with only settled leagues: %v", err)
	}
}
