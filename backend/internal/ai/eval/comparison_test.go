// Checks comparison inputs and plan validation, including CLI runs that report success
// without an accepted plan.

package eval

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/runner"
)

type sampleRunner struct {
	lookup bool
	reject bool
	omit   bool
	rename string
}

func (r sampleRunner) Run(ctx context.Context, s ai.DecisionSession) (ai.DecisionResult, error) {
	if r.omit {
		return ai.DecisionResult{Accepted: true}, nil
	}
	client, err := (runner.MCPClient{}).Connect(ctx, s.MCPURL, s.CapabilityToken)
	if err != nil {
		return ai.DecisionResult{}, err
	}
	defer client.Close()
	_, _, err = client.Call(ctx, ai.ToolCall{Name: "get_decision_context", Arguments: []byte(`{"requestId":"sample"}`)})
	if err != nil {
		return ai.DecisionResult{}, err
	}
	if r.lookup {
		if _, _, err := client.Call(ctx, ai.ToolCall{Name: "get_unit_details", Arguments: []byte(`{"requestId":"sample","unitIds":["ranger"]}`)}); err != nil {
			return ai.DecisionResult{}, err
		}
	}
	weight := int64(1000)
	if r.reject {
		weight = -1
	}
	payload, _ := json.Marshal(map[string]any{"requestId": "sample", "idempotencyKey": "once", "expectedVersion": 1, "targetTick": 20, "name": r.rename, "rationale": "Visible facts support this choice.", "actionWeights": map[string]int64{"attack": weight}, "reserveGold": 0, "spendOrder": []string{}, "constructionPriorities": []any{}, "recruitmentPriorities": []string{}})
	var accepted bool
	for i := 0; i < 3; i++ {
		_, accepted, err = client.Call(ctx, ai.ToolCall{Name: "submit_strategy_plan", Arguments: payload})
		if err != nil {
			return ai.DecisionResult{}, err
		}
		if accepted {
			break
		}
	}
	return ai.DecisionResult{Accepted: accepted}, nil
}
func comparisonCase() Case {
	return Case{ID: "sample", RequestID: "sample", Category: "normal", RulesVersion: "v1", ContextJSON: `{"context":{"requestId":"sample","targetTick":20,"expectedVersion":1,"legal":{"actionKinds":["attack"]},"memory":[{"targetTick":10,"status":"accepted"}]}}`}
}
func TestCompareUsesValidatorAndRejectsFalseCLISuccess(t *testing.T) {
	variants := []ComparisonVariant{
		{ID: "valid", Runtime: "api", NewRunner: func() ai.DecisionRunner { return sampleRunner{rename: "first"} }},
		{ID: "invalid", Runtime: "api", NewRunner: func() ai.DecisionRunner { return sampleRunner{reject: true} }},
		{ID: "false-cli-success", Runtime: "cli", NewRunner: func() ai.DecisionRunner { return sampleRunner{omit: true} }},
		{ID: "without-memory", Runtime: "cli", WithoutMemory: true, NewRunner: func() ai.DecisionRunner { return sampleRunner{rename: "second"} }},
	}
	options := ComparisonOptions{Repetitions: 2, MaxSteps: 4, MaxTokens: 100, Timeout: time.Second * 5}
	report, err := (Runner{}).Compare(context.Background(), []Case{comparisonCase()}, variants, options)
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed || report.Variants[0].Metrics.AcceptedRate != 1 || report.Variants[1].Metrics.AcceptedRate != 0 || report.Variants[2].Samples[0].ErrorCategory != "missing_submission" {
		t.Fatalf("report=%+v", report)
	}
	if report.Variants[1].Samples[0].ValidationFailures != 3 {
		t.Fatal("repair attempts not bounded or measured")
	}
	a, b := report.Variants[0].Samples[0], report.Variants[3].Samples[0]
	if a.ContextHash == b.ContextHash || a.StrategyFingerprint != b.StrategyFingerprint || len(report.Variants[0].Samples) != 2 {
		t.Fatal("ablation, canonical strategy fingerprint, or repetitions incorrect")
	}
	if report.Variants[0].Metrics.WinRate != nil {
		t.Fatal("decision acceptance cannot imply win rate")
	}
}
func TestComparisonRejectsInvalidInputsBeforeCallingRunner(t *testing.T) {
	v := []ComparisonVariant{{ID: "v", Runtime: "api", NewRunner: func() ai.DecisionRunner { t.Fatal("must not run"); return nil }}}
	opts := ComparisonOptions{Repetitions: 1, MaxSteps: 4, MaxTokens: 100, Timeout: time.Second}
	for _, cases := range [][]Case{nil, {{ID: "bad", ContextJSON: `{}`}}} {
		if _, err := (Runner{}).Compare(context.Background(), cases, v, opts); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
	if _, err := (Runner{}).Compare(context.Background(), []Case{comparisonCase()}, append(v, v...), opts); err == nil {
		t.Fatal("duplicate variant accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (Runner{}).Compare(ctx, []Case{comparisonCase()}, v, opts); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
