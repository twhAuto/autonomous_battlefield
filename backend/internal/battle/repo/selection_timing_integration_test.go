package repo

import (
	"context"
	"testing"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
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
	d, err = repo.ClaimModelDecision(ctx, "worker", []string{p.ProfileID}, time.Minute)
	if err != nil || d.ClaimedAt == nil {
		t.Fatalf("claim: %+v %v", d, err)
	}
	if err = repo.RecordDecisionContextServed(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	first, err := repo.GetDecision(ctx, p.ID)
	if err != nil || first.ContextServedAt == nil {
		t.Fatalf("context: %+v %v", first, err)
	}
	if err = repo.RecordDecisionContextServed(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	d, _ = repo.GetDecision(ctx, p.ID)
	if !d.ContextServedAt.Equal(*first.ContextServedAt) {
		t.Fatal("context reread changed timestamp")
	}
	plan := controller.PlanProposal{Name: "hold"}
	accepted, err := repo.SubmitDecisionPlan(ctx, ai.PlanSubmission{RequestID: p.ID, ExpectedVersion: d.Version, TargetTick: 10, IdempotencyKey: "once", Plan: plan})
	if err != nil || !accepted.Accepted {
		t.Fatal(accepted, err)
	}
	d, _ = repo.GetDecision(ctx, p.ID)
	if d.ResolvedAt == nil || d.RunnerCompletedAt != nil {
		t.Fatal("acceptance confused with worker completion")
	}
	resolved := *d.ResolvedAt
	if err = repo.RecordDecisionUsage(ctx, p.ID, ai.Usage{}, map[string]any{"cliProcessStartMs": int64(1)}); err != nil {
		t.Fatal(err)
	}
	d, _ = repo.GetDecision(ctx, p.ID)
	if d.RunnerCompletedAt == nil || !d.ResolvedAt.Equal(resolved) || d.ClaimedAt.Before(d.CreatedAt) || d.ResolvedAt.Before(*d.ClaimedAt) {
		t.Fatalf("timing: %+v", d)
	}
	// Retry reuse must not cross a model/effort change.
	p.ID = randomUUID(t)
	p.Attempt = 2
	p.ReasoningEffort = "high"
	d, err = repo.CreateOrReuseDecision(ctx, p)
	if err != nil || d.Status != "pending" {
		t.Fatalf("different effort reused: %+v %v", d, err)
	}

	// A lease reclaim starts a new execution breakdown and clears the old context.
	claimed, err := repo.ClaimModelDecision(ctx, "worker", []string{p.ProfileID}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.RecordDecisionContextServed(ctx, claimed.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `UPDATE battle_decisions SET lease_expires_at=now()-interval '1 second' WHERE id=$1`, claimed.ID); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := repo.ClaimModelDecision(ctx, "replacement", []string{p.ProfileID}, time.Minute)
	if err != nil || reclaimed.ClaimedAt == nil || !reclaimed.ClaimedAt.After(*claimed.ClaimedAt) || reclaimed.ContextServedAt != nil {
		t.Fatalf("reclaim: %+v %v", reclaimed, err)
	}
	if err = repo.FinishDecisionFailure(ctx, reclaimed.ID, "timeout", ai.Usage{}, map[string]any{"runnerDurationMs": 10}); err != nil {
		t.Fatal(err)
	}
	failed, _ := repo.GetDecision(ctx, reclaimed.ID)
	if failed.ResolvedAt == nil || failed.RunnerCompletedAt == nil || failed.Status != "failed" {
		t.Fatalf("failed timing: %+v", failed)
	}
	if ok, err := repo.ResolveDecisionFallback(ctx, reclaimed.ID, failed.Version, plan, "previous_plan", "timeout"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	p.ID = randomUUID(t)
	p.Attempt = 3
	p.ReasoningEffort = "low"
	d, err = repo.CreateOrReuseDecision(ctx, p)
	if err != nil || d.Status != "accepted" || d.ClaimedAt != nil {
		t.Fatalf("retry: %+v %v", d, err)
	}
}
