package mcpserver

import (
	"context"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai/capability"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai/runner"
)

// Opt-in: sends only synthetic battle data using the saved Codex (ChatGPT) login,
// verifying that the pinned CLI release still completes a decision through MCP.
func TestCodexLiveDecision(t *testing.T) {
	if os.Getenv("BATTLEFIELD_LIVE_CODEX") != "1" {
		t.Skip("set BATTLEFIELD_LIVE_CODEX=1 for a subscription smoke test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	record := ai.DecisionRecord{ID: "synthetic-codex-decision", Version: 1, TargetTick: 73, Status: "running", Runtime: "cli", Observation: ai.DecisionContext{SchemaVersion: ai.ContextSchema, RequestID: "synthetic-codex-decision", TargetTick: 73, ExpectedVersion: 1, Economy: ai.EconomyContext{Gold: 150}, Legal: ai.LegalChoices{ActionKinds: []string{"attack", "defend"}}}}
	repository := &fakeRepository{record: record}
	server := httptest.NewServer(New(repository, "synthetic-secret", nil).Handler())
	defer server.Close()
	token, err := capability.Mint("synthetic-secret", record.ID, time.Now().Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	result, err := (runner.CLIRunner{Provider: "codex-cli", Model: "gpt-5.6-luna"}).Run(ctx, ai.DecisionSession{RequestID: record.ID, CapabilityToken: token, MCPURL: server.URL})
	if err != nil || !result.Accepted {
		t.Fatalf("runner error=%v category=%s", err, result.ErrorCategory)
	}
	if repository.submitted.TargetTick != 73 || len(repository.submitted.Plan.ActionWeights) == 0 {
		t.Fatal("no substantive MCP plan for synthetic context tick")
	}
}
