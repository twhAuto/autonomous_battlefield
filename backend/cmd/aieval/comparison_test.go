// Exercises comparison command configuration, live-provider gates, runner factories, and
// knowledge ablation.

package main

import (
	"github.com/twh09/autonomous_battlefield/backend/internal/testutil"

	"encoding/json"
	aieval "github.com/twh09/autonomous_battlefield/backend/internal/ai/eval"
	"os"
	"path/filepath"
	"testing"
)

func TestFreeComparisonCommand(t *testing.T) {
	root := filepath.Join("..", "..", "evals")
	output := filepath.Join(t.TempDir(), "comparison.json")
	err := run([]string{"-mode", "compare", "-cases", filepath.Join(root, "comparison-cases"), "-comparison-config", filepath.Join(root, "comparison.yaml"), "-pricing", filepath.Join(root, "pricing.yaml"), "-repetitions", "2", "-json", output})
	testutil.Require(t, err == nil, func() { t.Fatal(err) })
	data, err := os.ReadFile(output)
	testutil.Require(t, err == nil, func() { t.Fatal(err) })
	var report aieval.ComparisonReport
	testutil.Require(t, !(json.Unmarshal(data, &report) != nil || !report.Passed || len(report.Variants) != 3 || len(report.Variants[0].Samples) != 8), func() { t.Fatalf("invalid report: %s", data) })
}
func TestComparisonLiveGatesAndFactories(t *testing.T) {
	prices := aieval.PriceBook{Models: []aieval.ModelPrice{{Model: "test"}}}
	v := comparisonVariantConfig{ID: "v", Provider: "openai", Model: "test"}
	t.Setenv("AI_EVAL_ALLOW_LIVE", "")
	{
		_, err := makeComparisonVariant(v, prices)
		testutil.Require(t, err != nil, func() { t.Fatal("live without opt-in") })
	}
	t.Setenv("AI_EVAL_ALLOW_LIVE", "1")
	t.Setenv("AI_EVAL_ALLOW_PAID", "")
	{
		_, err := makeComparisonVariant(v, prices)
		testutil.Require(t, err != nil, func() { t.Fatal("paid without opt-in") })
	}
	t.Setenv("AI_EVAL_ALLOW_PAID", "1")
	t.Setenv("OPENAI_API_KEY", "")
	{
		_, err := makeComparisonVariant(v, prices)
		testutil.Require(t, err != nil, func() { t.Fatal("missing credentials") })
	}
	t.Setenv("OPENAI_API_KEY", "synthetic")
	t.Setenv("GEMINI_API_KEY", "synthetic")
	for _, provider := range []string{"openai", "gemini", "codex-cli", "antigravity-cli", "scripted"} {
		v.Provider = provider
		got, err := makeComparisonVariant(v, prices)
		testutil.Require(t, !(err != nil || got.NewRunner() == nil), func() { t.Fatalf("factory %s: %v", provider, err) })
	}
	for _, v := range []comparisonVariantConfig{{Provider: "unknown", Model: "test"}, {Provider: "openai", Model: "unknown"}, {Provider: "codex-cli"}} {
		{
			_, err := makeComparisonVariant(v, prices)
			testutil.Require(t, err != nil, func() { t.Fatal("invalid variant accepted") })
		}
	}
}

func TestComparisonKnowledgeAblation(t *testing.T) {
	output := filepath.Join(t.TempDir(), "comparison.json")
	{
		err := run([]string{"-mode", "compare", "-cases", "../../evals/comparison-cases", "-comparison-config", "../../evals/comparison.yaml", "-pricing", "../../evals/pricing.yaml", "-repetitions", "1", "-json", output})
		testutil.Require(t, err == nil, func() { t.Fatal(err) })
	}
	data, err := os.ReadFile(output)
	testutil.Require(t, err == nil, func() { t.Fatal(err) })
	var report aieval.ComparisonReport
	{
		err = json.Unmarshal(data, &report)
		testutil.Require(t, err == nil, func() { t.Fatal(err) })
	}
	for _, v := range report.Variants {
		for _, s := range v.Samples {
			if s.ID != "large-roster" {
				continue
			}
			if v.WithoutKnowledge {
				testutil.Require(t, s.Knowledge == nil, func() { t.Fatal("ablation retained knowledge") })
			} else {
				testutil.Require(t, !(s.Knowledge == nil || len(s.Knowledge.SelectedIDs) != 16 || !s.Knowledge.Truncated), func() { t.Fatal("missing bounded knowledge") })
			}
		}
	}
}
