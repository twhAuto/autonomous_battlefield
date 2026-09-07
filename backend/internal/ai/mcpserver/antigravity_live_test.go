// Opt-in live Antigravity integration check for a complete decision through the MCP tools.

package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/runner"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// Explicit opt-in: uses the local subscription login and synthetic data only.
func TestAntigravityLiveDecision(t *testing.T) {
	if os.Getenv("BATTLEFIELD_LIVE_AGY") != "1" {
		t.Skip("set BATTLEFIELD_LIVE_AGY=1 for a subscription CLI smoke test")
	}
	record := ai.DecisionRecord{ID: "synthetic-decision", Version: 1, TargetTick: 73, Status: "running", Observation: ai.DecisionContext{RequestID: "synthetic-decision", TargetTick: 73, ExpectedVersion: 1, Economy: ai.EconomyContext{Gold: 150}, Legal: ai.LegalChoices{ActionKinds: []string{"attack", "defend"}}}}
	// A larger synthetic view checks that metadata remains usable with a
	// realistically sized response; no recorded battle data is used.
	for i := 0; i < 24; i++ {
		record.Observation.View.State.Units = append(record.Observation.View.State.Units, game.Unit{ID: fmt.Sprintf("synthetic-%d", i), FactionID: "synthetic-faction", Type: "vanguard", Health: 100, MaxHealth: 100})
	}
	record.Runtime = "cli"
	repository := &fakeRepository{record: record}
	server := httptest.NewServer(New(repository, "synthetic-secret", nil).Handler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	token, err := ai.MintCapability("synthetic-secret", record.ID, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	executor := &liveCLIExecutor{}
	result, err := (runner.CLIRunner{Executor: executor, Provider: "antigravity-cli", Model: "gemini-3.7-flash-medium"}).Run(ctx, ai.DecisionSession{RequestID: record.ID, CapabilityToken: token, MCPURL: server.URL})
	if err != nil {
		t.Fatalf("runner error: %v category=%s", err, result.ErrorCategory)
	}
	var envelope struct {
		Response string `json:"response"`
	}
	_ = json.Unmarshal(executor.output, &envelope)
	t.Logf("synthetic CLI response: %s", envelope.Response)
	if repository.submitted.TargetTick != 73 || len(repository.submitted.Plan.ActionWeights) == 0 {
		t.Fatalf("no substantive plan for the context tick: %+v", repository.submitted)
	}
	t.Logf("accepted synthetic plan %q for tick %d", repository.submitted.Plan.Name, repository.submitted.TargetTick)
}

type liveCLIExecutor struct{ output []byte }

func (e *liveCLIExecutor) Run(ctx context.Context, name string, args, env []string, dir string) ([]byte, error) {
	out, err := (runner.OSCommandExecutor{}).Run(ctx, name, args, env, dir)
	e.output = out
	return out, err
}
