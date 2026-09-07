// Exercises the versioned evaluation corpus, isolated provider cases, report aggregation,
// and omission of sensitive payloads.

package eval

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestVersionedCorpusPasses(t *testing.T) {
	root := filepath.Join("..", "..", "..", "evals")
	cases, err := LoadCases(filepath.Join(root, "cases"))
	if err != nil {
		t.Fatal(err)
	}
	prices, err := LoadPriceBook(filepath.Join(root, "pricing.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	report := (Runner{CorpusVersion: "2026-09-03.v1", Prices: prices, Now: func() time.Time { return now }}).Run(context.Background(), cases)
	if !report.Passed || len(report.Cases) < 8 {
		t.Fatalf("report passed=%t cases=%d: %#v", report.Passed, len(report.Cases), report.Cases)
	}
	for _, result := range report.Cases {
		if result.Model == "scripted" && result.EstimatedCostUSD == nil {
			t.Fatalf("case %s has no configured cost", result.ID)
		}
	}
}

func TestReportsContainNoSensitiveCasePayloads(t *testing.T) {
	report := Report{SchemaVersion: ReportSchema, Passed: false, Cases: []CaseReport{{ID: "case", SafetyFailures: []string{"safe summary"}}}}
	directory := t.TempDir()
	jsonPath := filepath.Join(directory, "report.json")
	junitPath := filepath.Join(directory, "report.xml")
	if err := WriteJSON(jsonPath, report); err != nil {
		t.Fatal(err)
	}
	if err := WriteJUnit(junitPath, report); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{jsonPath, junitPath} {
		payload, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(payload) == 0 {
			t.Fatalf("empty report %s", path)
		}
	}
}

func TestPaidProviderUsesTheVersionedIsolatedCase(t *testing.T) {
	root := filepath.Join("..", "..", "..", "evals")
	cases, err := LoadCases(filepath.Join(root, "cases"))
	if err != nil {
		t.Fatal(err)
	}
	var selected Case
	for _, value := range cases {
		if value.ID == "normal-valid-plan" {
			selected = value
			break
		}
	}
	if selected.ID == "" {
		t.Fatal("normal provider case is missing")
	}
	provider := &scriptedProvider{turns: selected.Turns}
	report := (Runner{CorpusVersion: "2026-09-03.v1", Prices: PriceBook{Version: "test", Models: []ModelPrice{{Model: "paid-test"}}}}).RunProvider(
		context.Background(), []Case{selected}, provider, "openai", "paid-test", selected.MaxSteps, selected.MaxTokens,
	)
	if !report.Passed || len(report.Cases) != 1 || report.Cases[0].Profile != "openai" || report.Cases[0].Outcome != "accepted" {
		t.Fatalf("provider report = %#v", report)
	}
}

func TestReportMetricsAggregatesOutcomes(t *testing.T) {
	firstCost := 1.25
	secondCost := 0.75
	metrics := reportMetrics([]CaseReport{
		{Accepted: true, DurationMS: 10, ToolCalls: []string{"submit_strategy_plan"}, StrategyFingerprint: "strategy-a", EstimatedCostUSD: &firstCost},
		{FallbackCategory: "invalid_plan", DurationMS: 20, ToolCalls: []string{"submit_strategy_plan"}, StrategyFingerprint: "strategy-a"},
		{FallbackCategory: "timeout", DurationMS: 30, StrategyFingerprint: "strategy-b", EstimatedCostUSD: &secondCost},
	})
	if metrics.CaseCount != 3 || metrics.StrategyDiversity != 2 {
		t.Fatalf("metric counts = %#v", metrics)
	}
	assertClose := func(name string, got, want float64) {
		t.Helper()
		if math.Abs(got-want) > 1e-9 {
			t.Fatalf("%s = %v, want %v", name, got, want)
		}
	}
	assertClose("accepted rate", metrics.AcceptedRate, 1.0/3.0)
	assertClose("invalid plan rate", metrics.InvalidPlanRate, 0.5)
	assertClose("fallback rate", metrics.FallbackRate, 2.0/3.0)
	assertClose("average latency", metrics.AverageLatencyMS, 20)
	assertClose("total cost", metrics.TotalCostUSD, 2)
	if empty := reportMetrics(nil); empty != (ReportMetrics{}) {
		t.Fatalf("empty metrics = %#v", empty)
	}
}

func TestRejectsDuplicateAndInvalidCases(t *testing.T) {
	directory := t.TempDir()
	invalid := []byte("schemaVersion: wrong\nid: bad\ncategory: normal\nrequestId: request\nmaxSteps: 1\nmaxTokens: 1\ncapability: valid\nexpected: {maximumToolCalls: 0}\n")
	if err := os.WriteFile(filepath.Join(directory, "bad.yaml"), invalid, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCases(directory); err == nil {
		t.Fatal("invalid schema version accepted")
	}
}
