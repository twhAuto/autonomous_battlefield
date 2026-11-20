// Exercises comparison command configuration, live-provider gates, runner factories, and
// knowledge ablation.

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	aieval "github.com/twhAuto/autonomous_battlefield/backend/internal/ai/eval"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai/runner"
)

func TestComparisonContextDeliveryAndEffort(t *testing.T) {
	t.Setenv("AI_EVAL_ALLOW_LIVE", "1")
	for _, provider := range []string{"codex-cli", "claude-cli"} {
		v, err := makeComparisonVariant(comparisonVariantConfig{ID: "prefetch", Provider: provider, Model: "test", ReasoningEffort: "low", PrefetchContext: true}, aieval.PriceBook{})
		if err != nil {
			t.Fatal(err)
		}
		r := v.NewRunner().(runner.CLIRunner)
		if !r.PrefetchContext || r.ReasoningEffort != "low" || !v.PrefetchContext || v.ReasoningEffort != "low" {
			t.Fatal("comparison silently changed delivery or reasoning settings")
		}
	}
	for _, v := range []comparisonVariantConfig{
		{Provider: "scripted", PrefetchContext: true},
		{Provider: "openai", ReasoningEffort: "low"},
		{Provider: "codex-cli", Model: "test", ReasoningEffort: "invalid"},
		{Provider: "antigravity-cli", Model: "test", ReasoningEffort: "low"},
	} {
		if _, err := makeComparisonVariant(v, aieval.PriceBook{}); err == nil {
			t.Fatalf("unsupported CLI options accepted for %s", v.Provider)
		}
	}
}

func TestFreeComparisonCommand(t *testing.T) {
	root := filepath.Join("..", "..", "evals")
	output := filepath.Join(t.TempDir(), "comparison.json")
	err := run([]string{"-mode", "compare", "-cases", filepath.Join(root, "comparison-cases"), "-comparison-config", filepath.Join(root, "comparison.yaml"), "-pricing", filepath.Join(root, "pricing.yaml"), "-repetitions", "2", "-json", output})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var report aieval.ComparisonReport
	if json.Unmarshal(data, &report) != nil || !report.Passed || len(report.Variants) != 3 || len(report.Variants[0].Samples) != 8 {
		t.Fatalf("invalid report: %s", data)
	}
}
func TestComparisonLiveGatesAndFactories(t *testing.T) {
	prices := aieval.PriceBook{Models: []aieval.ModelPrice{{Model: "test"}}}
	v := comparisonVariantConfig{ID: "v", Provider: "openai", Model: "test"}
	t.Setenv("AI_EVAL_ALLOW_LIVE", "")
	{
		_, err := makeComparisonVariant(v, prices)
		if err == nil {
			t.Fatal("live without opt-in")
		}
	}
	t.Setenv("AI_EVAL_ALLOW_LIVE", "1")
	t.Setenv("AI_EVAL_ALLOW_PAID", "")
	{
		_, err := makeComparisonVariant(v, prices)
		if err == nil {
			t.Fatal("paid without opt-in")
		}
	}
	t.Setenv("AI_EVAL_ALLOW_PAID", "1")
	t.Setenv("OPENAI_API_KEY", "")
	{
		_, err := makeComparisonVariant(v, prices)
		if err == nil {
			t.Fatal("missing credentials")
		}
	}
	t.Setenv("OPENAI_API_KEY", "synthetic")
	t.Setenv("GEMINI_API_KEY", "synthetic")
	for _, provider := range []string{"openai", "gemini", "codex-cli", "antigravity-cli", "claude-cli", "scripted"} {
		v.Provider = provider
		got, err := makeComparisonVariant(v, prices)
		if err != nil || got.NewRunner() == nil {
			t.Fatalf("factory %s: %v", provider, err)
		}
	}
	for _, v := range []comparisonVariantConfig{{Provider: "unknown", Model: "test"}, {Provider: "openai", Model: "unknown"}, {Provider: "codex-cli"}} {
		{
			_, err := makeComparisonVariant(v, prices)
			if err == nil {
				t.Fatal("invalid variant accepted")
			}
		}
	}
}

func TestComparisonKnowledgeAblation(t *testing.T) {
	output := filepath.Join(t.TempDir(), "comparison.json")
	if err := run([]string{"-mode", "compare", "-cases", "../../evals/comparison-cases", "-comparison-config", "../../evals/comparison.yaml", "-pricing", "../../evals/pricing.yaml", "-repetitions", "1", "-json", output}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var report aieval.ComparisonReport
	if err = json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	for _, v := range report.Variants {
		for _, s := range v.Samples {
			if s.ID == "large-roster" {
				checkLargeRosterKnowledge(t, v.WithoutKnowledge, s.Knowledge)
			}
		}
	}
}

// checkLargeRosterKnowledge requires the ablated variant to carry no knowledge and the
// others to carry 16 cards, truncated from the larger roster.
func checkLargeRosterKnowledge(t *testing.T, withoutKnowledge bool, knowledge *aieval.KnowledgeReport) {
	t.Helper()
	if withoutKnowledge {
		if knowledge != nil {
			t.Fatal("ablation retained knowledge")
		}
		return
	}
	if knowledge == nil || len(knowledge.SelectedIDs) != 16 || !knowledge.Truncated {
		t.Fatal("missing bounded knowledge")
	}
}
