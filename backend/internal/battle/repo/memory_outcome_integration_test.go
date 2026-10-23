package repo

import (
	"context"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
)

func TestFactionMemoryOutcomesReadRecordedEvents(t *testing.T) {
	ctx, db := integrationDatabase(t)
	battleID, factionID := randomUUID(t), randomUUID(t)
	t.Cleanup(func() { _, _ = db.Exec(context.Background(), `DELETE FROM battle_runs WHERE id=$1`, battleID) })
	repo := NewPostgres(db)
	createAndClaimBattle(t, ctx, repo, battleID)
	p := ai.CreateDecisionParams{ID: randomUUID(t), BattleID: battleID, Attempt: 1, FactionID: factionID, TargetTick: 10, ControllerType: "scripted", PromptVersion: "test", ToolSchemaVersion: ai.ToolSchema, ObservationHash: "outcome", Observation: ai.DecisionContext{SchemaVersion: ai.ContextSchema}}
	if _, err := repo.CreateOrReuseDecision(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE battle_decisions SET status='accepted', resolved_plan='{"name":"hold"}' WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	events := []struct {
		tick    int
		kind    string
		payload string
	}{
		{9, "unit_destroyed", `{"factionId":"` + factionID + `"}`},
		{12, "production_skipped", `{"factionId":"` + factionID + `","kind":"construction","reason":"maximum_reached"}`},
		{13, "unit_destroyed", `{"factionId":"enemy"}`},
		{14, "unit_moved", `{"factionId":"` + factionID + `"}`},
		{20, "unit_recruited", `{"factionId":"` + factionID + `"}`},
	}
	for i, event := range events {
		if _, err := db.Exec(ctx, `INSERT INTO battle_events (battle_id, attempt, sequence, tick, type, payload) VALUES ($1, 1, $2, $3, $4, $5::jsonb)`, battleID, i+1, event.tick, event.kind, event.payload); err != nil {
			t.Fatal(err)
		}
	}
	memory, err := repo.ListFactionMemory(ctx, battleID, 1, factionID, 20)
	if err != nil || len(memory) != 1 || memory[0].Outcome == nil {
		t.Fatalf("memory=%+v err=%v", memory, err)
	}
	outcome := *memory[0].Outcome
	if outcome.ThroughTick != 19 || outcome.EnemyUnitsDestroyed != 1 || outcome.UnitsLost != 0 || outcome.UnitsTrained != 0 || len(outcome.Skipped) != 1 || outcome.Skipped[0] != (ai.SkippedOrders{Kind: "construction", Reason: "maximum_reached", Count: 1}) {
		t.Fatalf("outcome = %+v", outcome)
	}
}
