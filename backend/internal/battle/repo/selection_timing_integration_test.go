package repo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/game/controller"
)

func TestDecisionSelectionAndTimingLifecycle(t *testing.T) {
	ctx, db := integrationDatabase(t)
	battleID := randomUUID(t)
	t.Cleanup(func() { _, _ = db.Exec(context.Background(), `DELETE FROM battle_runs WHERE id=$1`, battleID) })
	repo := NewPostgres(db)
	createAndClaimBattle(t, ctx, repo, battleID)
	p := ai.CreateDecisionParams{ID: randomUUID(t), BattleID: battleID, Attempt: 1, FactionID: randomUUID(t), TargetTick: 10, ControllerType: "model", ProfileID: "timing-test", Provider: "codex-cli", Runtime: "cli", ModelIdentifier: "fast", ReasoningEffort: "low", PromptVersion: "test", ToolSchemaVersion: ai.ToolSchema, ObservationHash: "same", Observation: ai.DecisionContext{SchemaVersion: ai.ContextSchema}}
	d, err := repo.CreateOrReuseDecision(ctx, p)
	if err != nil || d.ReasoningEffort != "low" || d.ClaimedAt != nil {
		t.Fatalf("create: %+v %v", d, err)
	}
	d, err = repo.ClaimModelDecision(ctx, "worker", []string{p.ProfileID}, true, time.Minute)
	if err != nil || d.ClaimedAt == nil {
		t.Fatalf("claim: %+v %v", d, err)
	}
	assertContextAndAcceptanceTiming(t, ctx, repo, d)
	plan := controller.PlanProposal{Name: "hold"}
	// Retry reuse must not cross a model/effort change.
	p.ID = randomUUID(t)
	p.Attempt = 2
	p.ReasoningEffort = "high"
	d, err = repo.CreateOrReuseDecision(ctx, p)
	if err != nil || d.Status != "pending" {
		t.Fatalf("different effort reused: %+v %v", d, err)
	}

	// A lease reclaim starts a new execution breakdown and clears the old context.
	claimed, err := repo.ClaimModelDecision(ctx, "worker", []string{p.ProfileID}, true, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.RecordDecisionContextServed(ctx, claimed.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `UPDATE battle_decisions SET lease_expires_at=now()-interval '1 second' WHERE id=$1`, claimed.ID); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := repo.ClaimModelDecision(ctx, "replacement", []string{p.ProfileID}, true, time.Minute)
	if err != nil || reclaimed.ClaimedAt == nil || !reclaimed.ClaimedAt.After(*claimed.ClaimedAt) || reclaimed.ContextServedAt != nil {
		t.Fatalf("reclaim: %+v %v", reclaimed, err)
	}
	assertFailureAndFallbackTiming(t, ctx, repo, reclaimed, plan)
	p.ID = randomUUID(t)
	p.Attempt = 3
	p.ReasoningEffort = "low"
	d, err = repo.CreateOrReuseDecision(ctx, p)
	if err != nil || d.Status != "accepted" || d.ClaimedAt != nil {
		t.Fatalf("retry: %+v %v", d, err)
	}
}

// Workers that cannot run API model options must not lease API decisions.
func TestDecisionClaimSkipsAPIDecisionsWhenExcluded(t *testing.T) {
	ctx, db := integrationDatabase(t)
	battleID := randomUUID(t)
	t.Cleanup(func() { _, _ = db.Exec(context.Background(), `DELETE FROM battle_runs WHERE id=$1`, battleID) })
	repo := NewPostgres(db)
	createAndClaimBattle(t, ctx, repo, battleID)
	p := ai.CreateDecisionParams{ID: randomUUID(t), BattleID: battleID, Attempt: 1, FactionID: randomUUID(t), TargetTick: 10, ControllerType: "model", ProfileID: "api-claim-test", Provider: "anthropic", Runtime: "api", ModelIdentifier: "claude-opus-5-5", ReasoningEffort: "high", PromptVersion: "test", ToolSchemaVersion: ai.ToolSchema, ObservationHash: "api", Observation: ai.DecisionContext{SchemaVersion: ai.ContextSchema}}
	decision, err := repo.CreateOrReuseDecision(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.ClaimModelDecision(ctx, "older-worker", []string{p.ProfileID}, false, time.Minute); !errors.Is(err, ai.ErrNoDecision) {
		t.Fatalf("API decision leased to a worker without API model selection: %v", err)
	}
	if claimed, err := repo.ClaimModelDecision(ctx, "worker", []string{p.ProfileID}, true, time.Minute); err != nil || claimed.ID != decision.ID {
		t.Fatalf("claim: %+v %v", claimed, err)
	}
}

// A claim's lease ends with the decision window, however long the decision waited
// for a slot, and a decision too close to its deadline is not claimed at all.
func TestDecisionClaimEndsWithItsWindow(t *testing.T) {
	ctx, db := integrationDatabase(t)
	battleID := randomUUID(t)
	t.Cleanup(func() { _, _ = db.Exec(context.Background(), `DELETE FROM battle_runs WHERE id=$1`, battleID) })
	repo := NewPostgres(db)
	createAndClaimBattle(t, ctx, repo, battleID)
	p := ai.CreateDecisionParams{ID: randomUUID(t), BattleID: battleID, Attempt: 1, FactionID: randomUUID(t), TargetTick: 10, ControllerType: "model", ProfileID: "window-test", Provider: "codex-cli", Runtime: "cli", ModelIdentifier: "fast", PromptVersion: "test", ToolSchemaVersion: ai.ToolSchema, ObservationHash: "late", Observation: ai.DecisionContext{SchemaVersion: ai.ContextSchema}}
	late, err := repo.CreateOrReuseDecision(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `UPDATE battle_decisions SET created_at=now()-interval '40 seconds' WHERE id=$1`, late.ID); err != nil {
		t.Fatal(err)
	}
	p.ID, p.FactionID, p.ObservationHash = randomUUID(t), randomUUID(t), "expiring"
	expiring, err := repo.CreateOrReuseDecision(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `UPDATE battle_decisions SET created_at=now()-interval '55 seconds' WHERE id=$1`, expiring.ID); err != nil {
		t.Fatal(err)
	}
	claimed, err := repo.ClaimModelDecision(ctx, "worker", []string{p.ProfileID}, true, time.Minute)
	if err != nil || claimed.ID != late.ID || claimed.LeaseExpiresAt == nil {
		t.Fatalf("claim: %+v %v", claimed, err)
	}
	if want := claimed.CreatedAt.Add(time.Minute); !claimed.LeaseExpiresAt.Equal(want) {
		t.Fatalf("lease ends %v, want window end %v", claimed.LeaseExpiresAt, want)
	}
	// Five seconds left is under minClaimWindow, so the decision waits for its fallback.
	if _, err = repo.ClaimModelDecision(ctx, "worker", []string{p.ProfileID}, true, time.Minute); !errors.Is(err, ai.ErrNoDecision) {
		t.Fatalf("claimed a decision with too little window left: %v", err)
	}
	if record, err := repo.GetDecision(ctx, expiring.ID); err != nil || record.Status != "pending" {
		t.Fatalf("expiring decision: %+v %v", record, err)
	}
	// A two-second decision timeout leases for seven seconds, under minClaimWindow, yet a
	// fresh decision must still run.
	p.ID, p.FactionID, p.ObservationHash = randomUUID(t), randomUUID(t), "short-timeout"
	fresh, err := repo.CreateOrReuseDecision(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err = repo.ClaimModelDecision(ctx, "worker", []string{p.ProfileID}, true, 7*time.Second); err != nil || claimed.ID != fresh.ID {
		t.Fatalf("short lease claim: %+v %v", claimed, err)
	}
}

func assertContextAndAcceptanceTiming(t *testing.T, ctx context.Context, repo *Postgres, decision ai.DecisionRecord) {
	t.Helper()
	if err := repo.RecordDecisionContextServed(ctx, decision.ID); err != nil {
		t.Fatal(err)
	}
	first, err := repo.GetDecision(ctx, decision.ID)
	if err != nil || first.ContextServedAt == nil {
		t.Fatalf("context: %+v %v", first, err)
	}
	if err = repo.RecordDecisionContextServed(ctx, decision.ID); err != nil {
		t.Fatal(err)
	}
	d, _ := repo.GetDecision(ctx, decision.ID)
	if !d.ContextServedAt.Equal(*first.ContextServedAt) {
		t.Fatal("context reread changed timestamp")
	}
	plan := controller.PlanProposal{Name: "hold"}
	accepted, err := repo.SubmitDecisionPlan(ctx, ai.PlanSubmission{RequestID: decision.ID, ExpectedVersion: d.Version, TargetTick: 10, IdempotencyKey: "once", Plan: plan})
	if err != nil || !accepted.Accepted {
		t.Fatal(accepted, err)
	}
	d, _ = repo.GetDecision(ctx, decision.ID)
	if d.ResolvedAt == nil || d.RunnerCompletedAt != nil {
		t.Fatal("acceptance confused with worker completion")
	}
	resolved := *d.ResolvedAt
	if err = repo.RecordDecisionUsage(ctx, decision.ID, ai.Usage{}, map[string]any{"cliProcessStartMs": int64(1)}); err != nil {
		t.Fatal(err)
	}
	d, _ = repo.GetDecision(ctx, decision.ID)
	if d.RunnerCompletedAt == nil || !d.ResolvedAt.Equal(resolved) || d.ClaimedAt.Before(d.CreatedAt) || d.ResolvedAt.Before(*d.ClaimedAt) {
		t.Fatalf("timing: %+v", d)
	}
}

func assertFailureAndFallbackTiming(t *testing.T, ctx context.Context, repo *Postgres, reclaimed ai.DecisionRecord, plan controller.PlanProposal) {
	t.Helper()
	if err := repo.FinishDecisionFailure(ctx, reclaimed.ID, "timeout", ai.Usage{}, map[string]any{"runnerDurationMs": 10}); err != nil {
		t.Fatal(err)
	}
	failed, _ := repo.GetDecision(ctx, reclaimed.ID)
	if failed.ResolvedAt == nil || failed.RunnerCompletedAt == nil || failed.Status != "failed" {
		t.Fatalf("failed timing: %+v", failed)
	}
	if ok, err := repo.ResolveDecisionFallback(ctx, reclaimed.ID, failed.Version, plan, "previous_plan", "timeout"); err != nil || !ok {
		t.Fatal(ok, err)
	}
}
