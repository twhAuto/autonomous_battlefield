package repo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/battle/queue"
	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
)

func TestExpireBattlesAgainstPostgres(t *testing.T) {
	ctx, db := integrationDatabase(t)
	repository := NewPostgres(db)
	for _, test := range []struct {
		name        string
		status      string
		paused, old bool
	}{
		{"old paused queued", "queued", true, true}, {"old queued", "queued", false, true},
		{"old paused running", "running", true, true}, {"old running", "running", false, true},
		{"recent paused running", "running", true, false}, {"recent paused queued", "queued", true, false},
		{"completed", "completed", false, true}, {"stopped", "stopped", false, true}, {"failed", "failed", false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			testBattleExpiry(t, ctx, db, repository, test.status, test.paused, test.old)
		})
	}
}

func testBattleExpiry(t *testing.T, ctx context.Context, db *pgxpool.Pool, repository *Postgres, status string, paused, old bool) {
	t.Helper()
	id := randomUUID(t)
	t.Cleanup(func() { _, _ = db.Exec(context.Background(), `DELETE FROM battle_runs WHERE id=$1`, id) })
	scenario := repositoryTestScenario()
	if err := repository.CreateBattle(ctx, battleservice.CreateBattleParams{ID: id, ScenarioID: scenario.ID, ScenarioDigest: "digest", ScenarioSnapshot: scenario, RequestedTicks: 2}); err != nil {
		t.Fatal(err)
	}
	age := "30 minutes"
	if old {
		age = "61 minutes"
	}
	if _, err := db.Exec(ctx, `UPDATE battle_runs SET status=$2, paused=$3, created_at=now()-$4::interval, attempt=1, lease_owner=CASE WHEN $2='running' THEN 'expiry-test' END, lease_expires_at=CASE WHEN $2='running' THEN now()+interval '1 minute' END WHERE id=$1`, id, status, paused, age); err != nil {
		t.Fatal(err)
	}
	decisionStatus := "pending"
	if status == "running" {
		decisionStatus = "running"
	}
	decisionID := randomUUID(t)
	if _, err := db.Exec(ctx, `INSERT INTO battle_decisions (id,battle_id,attempt,faction_id,target_tick,status,controller_type,prompt_version,tool_schema_version,observation,observation_hash) VALUES ($1,$2,1,$3,1,$4,'scripted','test','test','{}','test')`, decisionID, id, randomUUID(t), decisionStatus); err != nil {
		t.Fatal(err)
	}
	if err := repository.ExpireBattles(ctx); err != nil {
		t.Fatal(err)
	}
	run, err := repository.GetBattle(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := repository.GetDecision(ctx, decisionID)
	if err != nil {
		t.Fatal(err)
	}
	if old && (status == "queued" || status == "running") {
		assertExpiredBattle(t, ctx, repository, id, run, decision)
	} else if decision.Status != decisionStatus || run.Status != status || run.Paused != paused {
		t.Fatalf("unaffected battle changed: %#v", run)
	}
	if err := repository.ExpireBattles(ctx); err != nil {
		t.Fatal(err)
	}
}

func assertExpiredBattle(t *testing.T, ctx context.Context, repository *Postgres, id string, run *battleservice.BattleRun, decision ai.DecisionRecord) {
	t.Helper()
	if decision.Status != "failed" || decision.ErrorCategory != "battle_stopped" {
		t.Fatalf("decision not cancelled: %#v", decision)
	}
	if run.Status != "failed" || run.ErrorCode == nil || *run.ErrorCode != "battle_timeout" || run.Paused {
		t.Fatalf("expired battle: %#v", run)
	}
	if err := repository.HeartbeatBattle(ctx, queue.Lease{BattleID: id, Attempt: 1, Owner: "expiry-test"}, time.Minute); !errors.Is(err, queue.ErrLeaseLost) {
		t.Fatalf("stale lease: %v", err)
	}
}
