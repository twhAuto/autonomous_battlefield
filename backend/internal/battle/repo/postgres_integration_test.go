package repo

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twh09/autonomous_battlefield/backend/internal/battle/engine/worker"
	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/server/service"
	battle "github.com/twh09/autonomous_battlefield/backend/internal/battle/simulation"
	"github.com/twh09/autonomous_battlefield/backend/internal/battle/simulation/controller"
	"github.com/twh09/autonomous_battlefield/backend/internal/postgres"
)

func TestBattleRepositoryLifecycleAgainstPostgres(t *testing.T) {
	ctx, db := integrationDatabase(t)

	battleID := randomUUID(t)
	t.Cleanup(func() { _, _ = db.Exec(context.Background(), `DELETE FROM battle_runs WHERE id = $1`, battleID) })
	repo := NewPostgres(db)
	job := createAndClaimBattle(t, ctx, repo, battleID)
	if job.Lease.BattleID != battleID || job.Lease.Attempt != 1 {
		t.Fatalf("claimed job = %#v", job)
	}
	if _, err := repo.ClaimBattle(ctx, "worker-2", 30*time.Second, 3); err != worker.ErrNoBattleAvailable {
		t.Fatalf("second ClaimBattle() error = %v, want ErrNoBattleAvailable", err)
	}

	tick := battle.Transition{Tick: 1, Events: []battle.Event{{Sequence: 1, Tick: 1, Type: "tick_completed", Data: map[string]any{"tick": uint64(1)}}}}
	if err := repo.SaveTick(ctx, job.Lease, tick, 30*time.Second); err != nil {
		t.Fatalf("SaveTick() returned an error: %v", err)
	}
	events, err := repo.ListBattleEvents(ctx, battleID, 1, 0, 100)
	if err != nil {
		t.Fatalf("ListBattleEvents() returned an error: %v", err)
	}
	if len(events) != 1 || events[0].Sequence != 1 {
		t.Fatalf("events = %#v", events)
	}
	afterCursor, err := repo.ListBattleEvents(ctx, battleID, 1, 1, 100)
	if err != nil {
		t.Fatalf("ListBattleEvents(after cursor) returned an error: %v", err)
	}
	if len(afterCursor) != 0 {
		t.Fatalf("events after sequence 1 = %#v, want empty", afterCursor)
	}

	result := battle.Result{Reason: "tick_limit", TicksRun: 2}
	completion := battle.Transition{Tick: 2, Finished: true, Result: &result, Events: []battle.Event{{Sequence: 2, Tick: 2, Type: "battle_completed", Data: map[string]any{"reason": "tick_limit"}}}}
	if err := repo.CompleteBattle(ctx, job.Lease, completion); err != nil {
		t.Fatalf("CompleteBattle() returned an error: %v", err)
	}
	run, err := repo.GetBattle(ctx, battleID)
	if err != nil {
		t.Fatalf("GetBattle() returned an error: %v", err)
	}
	if run.Status != "completed" || run.CurrentTick != 2 || run.Result == nil || run.Result.Reason != "tick_limit" {
		t.Fatalf("run = %#v", run)
	}
	if err := repo.RequeueBattle(ctx, job.Lease); err != worker.ErrBattleLeaseLost {
		t.Fatalf("stale RequeueBattle() error = %v, want ErrBattleLeaseLost", err)
	}
}

func createAndClaimBattle(t *testing.T, ctx context.Context, repo *Postgres, battleID string) *worker.BattleJob {
	t.Helper()
	scenario := repositoryTestScenario()
	if err := repo.CreateBattle(ctx, battleservice.CreateBattleParams{ID: battleID, ScenarioID: scenario.ID, ScenarioDigest: "digest", ScenarioSnapshot: scenario, RequestedTicks: 2}); err != nil {
		t.Fatalf("CreateBattle() returned an error: %v", err)
	}
	job, err := repo.ClaimBattle(ctx, "worker-1", 30*time.Second, 3)
	if err != nil {
		t.Fatalf("ClaimBattle() returned an error: %v", err)
	}
	return job
}

func TestBattleRepositoryReclaimsExpiredLeaseAndRejectsStaleWorker(t *testing.T) {
	ctx, db := integrationDatabase(t)
	battleID := randomUUID(t)
	t.Cleanup(func() { _, _ = db.Exec(context.Background(), `DELETE FROM battle_runs WHERE id = $1`, battleID) })
	repo := NewPostgres(db)
	scenario := repositoryTestScenario()
	if err := repo.CreateBattle(ctx, battleservice.CreateBattleParams{ID: battleID, ScenarioID: scenario.ID, ScenarioDigest: "digest", ScenarioSnapshot: scenario, RequestedTicks: 2}); err != nil {
		t.Fatalf("CreateBattle() returned an error: %v", err)
	}
	first, err := repo.ClaimBattle(ctx, "worker-1", time.Millisecond, 3)
	if err != nil {
		t.Fatalf("first ClaimBattle() returned an error: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	second, err := repo.ClaimBattle(ctx, "worker-2", time.Second, 3)
	if err != nil {
		t.Fatalf("second ClaimBattle() returned an error: %v", err)
	}
	if second.Lease.Attempt != 2 || second.Lease.Owner != "worker-2" {
		t.Fatalf("reclaimed lease = %#v", second.Lease)
	}
	if err := repo.HeartbeatBattle(ctx, first.Lease, time.Second); err != worker.ErrBattleLeaseLost {
		t.Fatalf("stale HeartbeatBattle() error = %v, want ErrBattleLeaseLost", err)
	}
	if err := repo.RequeueBattle(ctx, second.Lease); err != nil {
		t.Fatalf("RequeueBattle() returned an error: %v", err)
	}
}

func TestBattleRepositoryFailsExhaustedAttempts(t *testing.T) {
	ctx, db := integrationDatabase(t)
	battleID := randomUUID(t)
	t.Cleanup(func() { _, _ = db.Exec(context.Background(), `DELETE FROM battle_runs WHERE id = $1`, battleID) })
	repo := NewPostgres(db)
	scenario := repositoryTestScenario()
	if err := repo.CreateBattle(ctx, battleservice.CreateBattleParams{ID: battleID, ScenarioID: scenario.ID, ScenarioDigest: "digest", ScenarioSnapshot: scenario, RequestedTicks: 2}); err != nil {
		t.Fatalf("CreateBattle() returned an error: %v", err)
	}
	if _, err := repo.ClaimBattle(ctx, "worker-1", time.Millisecond, 1); err != nil {
		t.Fatalf("ClaimBattle() returned an error: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if _, err := repo.ClaimBattle(ctx, "worker-2", time.Second, 1); err != worker.ErrNoBattleAvailable {
		t.Fatalf("second ClaimBattle() error = %v, want ErrNoBattleAvailable", err)
	}
	run, err := repo.GetBattle(ctx, battleID)
	if err != nil {
		t.Fatalf("GetBattle() returned an error: %v", err)
	}
	if run.Status != "failed" || run.ErrorCode == nil || *run.ErrorCode != "attempts_exhausted" {
		t.Fatalf("run = %#v, want attempts-exhausted failure", run)
	}
}

func integrationDatabase(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	if _, err := postgres.Migrate(ctx, url, 5*time.Second); err != nil {
		t.Fatalf("postgres.Migrate() returned an error: %v", err)
	}
	db, err := postgres.NewPostgresDB(ctx, url, postgres.Options{MaxConnections: 4, MinConnections: 0, MaxConnectionLifetime: time.Minute})
	if err != nil {
		t.Fatalf("postgres.NewPostgresDB() returned an error: %v", err)
	}
	t.Cleanup(db.Close)
	return ctx, db
}

func repositoryTestScenario() battle.Scenario {
	plan := controller.PlanProposal{Name: "attack", ActionWeights: map[string]int64{battle.ActionAttack: 1000}}
	return battle.Scenario{
		ID: "integration", RulesVersion: "1", DecisionIntervalTicks: 10, DecisionLeadTicks: 2,
		Rules: battle.CombatRules{MinimumDamage: 1, DefendAttackPercent: 50, DefendDamageTakenPercent: 50},
		Factions: []battle.FactionSetup{
			{ID: "faction-a", Name: "A", Units: []battle.UnitTemplate{{ID: "a", Health: 10, Attack: 5, Speed: 2}}, FallbackPlan: plan, ScriptedPlans: []controller.PlanProposal{plan}},
			{ID: "faction-b", Name: "B", Units: []battle.UnitTemplate{{ID: "b", Health: 10, Attack: 5, Speed: 1}}, FallbackPlan: plan, ScriptedPlans: []controller.PlanProposal{plan}},
		},
	}
}

func randomUUID(t *testing.T) string {
	t.Helper()
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		t.Fatalf("rand.Read() returned an error: %v", err)
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		bytes[0:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:16])
}
