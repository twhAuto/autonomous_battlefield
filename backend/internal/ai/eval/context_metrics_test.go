package eval

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
)

func TestCompactLookupEvaluation(t *testing.T) {
	r := game.Ruleset{ID: "test", Version: "1", Units: []game.UnitType{{ID: "ranger", Cost: 10, Attack: 20}}}
	c := ai.DecisionContext{SchemaVersion: ai.ContextSchema, RequestID: "sample", Legal: ai.LegalChoices{Units: r.Units}}
	payload, _ := json.Marshal(map[string]any{"context": c})
	fixture := Case{ID: "compact", RequestID: "sample", ContextJSON: string(payload), KnowledgeRuleset: &r, MaxSteps: 3, MaxTokens: 100, Submission: Submission{Accepted: true}, Expected: Expected{Accepted: true, MaximumToolCalls: 3}}
	for i, call := range []ScriptCall{{Name: "get_decision_context", Arguments: `{"requestId":"sample"}`}, {Name: "get_unit_details", Arguments: `{"requestId":"sample","unitIds":["ranger"]}`}, {Name: "submit_strategy_plan", Arguments: `{"requestId":"sample"}`}} {
		fixture.Turns = append(fixture.Turns, ScriptTurn{ToolCalls: []ScriptCall{call}, Usage: ai.Usage{InputTokens: int64(10 * (i + 1))}})
	}
	report := (Runner{}).Run(context.Background(), []Case{fixture})
	if !report.Passed || report.Metrics.TotalInputTokens != 60 || report.Metrics.LookupRate != 1 || report.Metrics.UnitDetailCalls != 1 || report.Cases[0].ContextBytes == 0 {
		t.Fatalf("%+v", report)
	}
	usage := report.Cases[0].Usage
	if usage == nil || usage.Scope != "api_calls" || usage.InputTokens != 60 || usage.ModelCalls == nil || *usage.ModelCalls != 3 {
		t.Fatal("detailed report usage missing", usage)
	}
	session := scriptedSession{value: fixture}
	for _, args := range []string{`{"requestId":"other","unitIds":["ranger"]}`, `{"requestId":"sample","unitIds":[]}`} {
		if payload, _, err := session.Call(context.Background(), ai.ToolCall{Name: "get_unit_details", Arguments: []byte(args)}); err == nil && !strings.Contains(string(payload), `"error"`) {
			t.Fatal("invalid lookup allowed")
		}
	}
}
func TestComparisonCompactProjectionAndAblation(t *testing.T) {
	c := ai.DecisionContext{SchemaVersion: ai.LegacyContextSchema, UnitReference: &ai.UnitReferenceSnapshot{Units: []ai.UnitDetail{{UnitID: "a", Card: &game.UnitCard{Guidance: "advice"}}}}}
	got := comparisonObservation(c, ComparisonVariant{CompactUnitContext: true, WithoutKnowledge: true})
	if got.SchemaVersion != ai.ContextSchema || got.UnitReference.Units[0].Card != nil || c.UnitReference.Units[0].Card == nil {
		t.Fatal("ablation changed original reference")
	}
}

func TestComparisonCountsActualLookupCalls(t *testing.T) {
	fixture := comparisonCase()
	c := caseIdentity(fixture)
	rules := game.Ruleset{ID: "r", Version: "1", Units: []game.UnitType{{ID: "ranger", Cost: 10}}}
	c.Legal.Units = rules.Units
	c.UnitReference = ai.FreezeUnitReference(rules, c)
	payload, _ := json.Marshal(map[string]any{"context": c})
	fixture.ContextJSON = string(payload)
	variants := []ComparisonVariant{{ID: "compact", Runtime: "api", CompactUnitContext: true, NewRunner: func() ai.DecisionRunner { return sampleRunner{rename: "lookup", lookup: true} }}}
	report, err := (Runner{}).Compare(context.Background(), []Case{fixture}, variants, ComparisonOptions{Repetitions: 1, MaxSteps: 4, MaxTokens: 100, Timeout: 5 * time.Second})
	if err != nil || !report.Passed || report.Variants[0].Metrics.UnitDetailCalls != 1 || report.Variants[0].Metrics.LookupRate != 1 || report.Variants[0].Samples[0].ContextBytes == 0 {
		t.Fatal(report, err)
	}
}
