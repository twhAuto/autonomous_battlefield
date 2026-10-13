package service

import (
	"context"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/telemetry"
)

func TestHistoryLinksOnlyRecordedDecisionTraces(t *testing.T) {
	t.Setenv("LANGFUSE_BASE_URL", "http://localhost:13000")
	t.Setenv("LANGFUSE_PROJECT_ID", "battlefield-agents")
	repo := &historyBattleRepo{fakeBattleRepo: &fakeBattleRepo{}, records: []ai.DecisionRecord{
		{ID: "new", ProviderMetadata: map[string]any{"aiTraceId": telemetry.DecisionTraceID("new")}},
		{ID: "legacy"}, {ID: "wrong", ProviderMetadata: map[string]any{"aiTraceId": "other"}},
	}}
	service := NewService(repo, &battleFactionRepo{}, &fakeScenarioCatalog{}, 10_000, Options{}).(DecisionHistoryService)
	history, err := service.ListDecisionHistory(context.Background(), "7f22e693-bacd-46bb-b942-64fa613cd388", 1)
	if err != nil || history[0].TraceURL != telemetry.DecisionTraceURL("new") || history[1].TraceURL != "" || history[2].TraceURL != "" {
		t.Fatal(history, err)
	}
}
