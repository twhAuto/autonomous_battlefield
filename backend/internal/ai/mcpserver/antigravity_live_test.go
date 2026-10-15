// Opt-in live Antigravity integration check for a complete decision through the MCP tools.

package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/agentview"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/capability"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/runner"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
)

// Explicit opt-in: uses the local subscription login and synthetic data only.
func TestAntigravityLiveDecision(t *testing.T) {
	if os.Getenv("BATTLEFIELD_LIVE_AGY") != "1" {
		t.Skip("set BATTLEFIELD_LIVE_AGY=1 for a subscription CLI smoke test")
	}
	record := ai.DecisionRecord{ID: "synthetic-decision", Version: 1, TargetTick: 73, Status: "running", Observation: ai.DecisionContext{RequestID: "synthetic-decision", TargetTick: 73, ExpectedVersion: 1, Economy: ai.EconomyContext{Gold: 150}, Legal: ai.LegalChoices{ActionKinds: []string{"attack", "defend"}}}}
	// A larger synthetic view checks that metadata remains usable with a
	// realistically sized response; no recorded battle data is used.
	for i := 0; i < 160; i++ {
		record.Observation.View.State.Units = append(record.Observation.View.State.Units, game.Unit{ID: fmt.Sprintf("synthetic-%d", i), FactionID: "synthetic-faction", Type: "vanguard", Health: 100, MaxHealth: 100})
	}
	record.Runtime = "cli"
	record.Observation.SchemaVersion = ai.ContextSchema
	rules := game.Ruleset{ID: "synthetic", Version: "1"}
	rules.Knowledge = &game.RulesKnowledge{Mechanics: "Synthetic transport check"}
	ids := []string{"vanguard", "ranger", "guard", "scout"}
	for _, id := range ids {
		rules.Units = append(rules.Units, game.UnitType{ID: id, Cost: 10, Health: 100, Attack: 20})
		rules.Knowledge.Cards = append(rules.Knowledge.Cards, game.UnitCard{UnitID: id, Role: "soldier", Guidance: strings.Repeat("\t", 980) + " Verification word: juniper."})
	}
	record.Observation.Legal.Units = rules.Units
	record.Observation.UnitReference = agentview.FreezeUnitReference(rules, record.Observation)

	repository := &fakeRepository{record: record}
	server := httptest.NewServer(New(repository, "synthetic-secret", nil).Handler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	token, err := capability.Mint("synthetic-secret", record.ID, time.Now().Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	executor := &liveCLIExecutor{}
	result, err := (runner.CLIRunner{Executor: executor, Provider: "antigravity-cli", Model: "gemini-3.8-flash-medium"}).Run(ctx, ai.DecisionSession{Doctrine: "For this synthetic transport verification, call get_unit_details for vanguard, ranger, guard, scout before submitting; retry with scout alone if the batch is too large. Include the verification word from scout's fetched guidance in your rationale. Do not read files.", RequestID: record.ID, CapabilityToken: token, MCPURL: server.URL})
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
	if !strings.Contains(strings.ToLower(repository.submitted.Plan.Rationale), "juniper") {
		t.Fatalf("four-unit lookup marker absent from rationale: %s", repository.submitted.Plan.Rationale)
	}
	t.Logf("accepted synthetic plan %q for tick %d", repository.submitted.Plan.Name, repository.submitted.TargetTick)
}

type liveCLIExecutor struct{ output []byte }

func (e *liveCLIExecutor) Run(ctx context.Context, name string, args, env []string, dir string) ([]byte, error) {
	for i, arg := range args {
		if arg == "--print" && i+1 < len(args) {
			args[i+1] += "\nSynthetic test acceptance requirement: first fetch get_unit_details for vanguard, ranger, guard, scout. If the batch exceeds the response budget, retry with scout alone. Follow nextGuidanceOffset using guidanceOffset if present. Your submitted rationale must quote the exact verification word at the end of scout guidance. If the result is inaccessible or offloaded, stop and report that problem instead of claiming verification."
		}
	}
	out, err := (runner.OSCommandExecutor{}).Run(ctx, name, args, env, dir)
	e.output = out
	return out, err
}
