package mcpserver

import (
	"context"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai/capability"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai/cliisolation"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai/runner"
)

// Opt-in: sends only synthetic battle data using the saved Claude subscription.
func TestClaudeLiveDecision(t *testing.T) {
	if os.Getenv("BATTLEFIELD_LIVE_CLAUDE") != "1" {
		t.Skip("set BATTLEFIELD_LIVE_CLAUDE=1 for a subscription smoke test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := cliisolation.CheckClaudeAuth(ctx); err != nil {
		t.Fatal(err)
	}
	record := ai.DecisionRecord{ID: "synthetic-claude-decision", Version: 1, TargetTick: 73, Status: "running", Runtime: "cli", Observation: ai.DecisionContext{SchemaVersion: ai.ContextSchema, RequestID: "synthetic-claude-decision", TargetTick: 73, ExpectedVersion: 1, Economy: ai.EconomyContext{Gold: 150}, Legal: ai.LegalChoices{ActionKinds: []string{"attack", "defend"}}}}
	repository := &fakeRepository{record: record}
	server := httptest.NewServer(New(repository, "synthetic-secret", nil).Handler())
	defer server.Close()
	token, err := capability.Mint("synthetic-secret", record.ID, time.Now().Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	result, err := (runner.CLIRunner{Provider: "claude-cli", Model: "sonnet"}).Run(ctx, ai.DecisionSession{RequestID: record.ID, CapabilityToken: token, MCPURL: server.URL})
	if err != nil || !result.Accepted {
		t.Fatalf("runner error=%v category=%s", err, result.ErrorCategory)
	}
	if repository.submitted.TargetTick != 73 || len(repository.submitted.Plan.ActionWeights) == 0 {
		t.Fatal("no substantive MCP plan for synthetic context tick")
	}
	if !result.Usage.TokensReported {
		t.Fatal("Claude did not report session usage")
	}
}
