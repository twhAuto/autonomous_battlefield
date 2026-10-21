package ai

import (
	"math"
	"testing"
)

func TestModelSelectionAllowlistAndDefaults(t *testing.T) {
	p := ModelProfile{Runtime: "cli", Provider: "codex-cli", Model: "fast", ModelOptions: []ModelOption{{ID: "fast", ReasoningEfforts: []string{"low", "high"}}, {ID: "deep", ReasoningEfforts: []string{"medium"}, DefaultReasoningEffort: "medium"}}}
	for _, tc := range []struct {
		model, effort, wantModel, wantEffort string
		valid                                bool
	}{
		{"", "", "fast", "", true}, {"fast", "low", "fast", "low", true}, {"deep", "", "deep", "medium", true},
		{"deep", "high", "", "", false}, {"unknown", "", "", "", false}, {"fast", `low\";shell=true`, "", "", false},
	} {
		model, effort, err := ResolveModelSelection(p, tc.model, tc.effort)
		if (err == nil) != tc.valid || model != tc.wantModel || effort != tc.wantEffort {
			t.Fatalf("%+v: %s %s %v", tc, model, effort, err)
		}
	}
	p.ModelOptions = nil
	if m, e, err := ResolveModelSelection(p, "", ""); err != nil || m != "fast" || e != "" {
		t.Fatal(m, e, err)
	}
	p.Runtime = "api"
	p.ModelLabel = "configured"
	if m, _, err := ResolveModelSelection(p, "", ""); err != nil || m != "configured" {
		t.Fatal(m, err)
	}
	if _, _, err := ResolveModelSelection(p, "configured", "low"); err == nil {
		t.Fatal("unsupported API effort accepted")
	}
}

func TestValidateModelOptions(t *testing.T) {
	for _, tc := range []struct {
		provider, runtime string
		options           []ModelOption
		valid             bool
	}{
		{"codex-cli", "cli", []ModelOption{{ID: "a", ReasoningEfforts: []string{"low", "high"}}}, true},
		{"claude-cli", "cli", []ModelOption{{ID: "a", ReasoningEfforts: []string{"low", "max"}}}, true},
		{"antigravity-cli", "cli", []ModelOption{{ID: "a"}}, true},
		{"antigravity-cli", "cli", []ModelOption{{ID: "a", ReasoningEfforts: []string{"low"}}}, false},
		{"openai", "api", []ModelOption{{ID: "a"}}, false},
		{"codex-cli", "cli", []ModelOption{{ID: ""}}, false},
		{"codex-cli", "cli", []ModelOption{{ID: "-flag"}}, false},
		{"codex-cli", "cli", []ModelOption{{ID: " a "}}, false},
		{"codex-cli", "cli", []ModelOption{{ID: "a"}, {ID: "a"}}, false},
		{"codex-cli", "cli", []ModelOption{{ID: "a", ReasoningEfforts: []string{"low", "low"}}}, false},
		{"codex-cli", "cli", []ModelOption{{ID: "a", DefaultReasoningEffort: "high"}}, false},
	} {
		if err := ValidateModelOptions(ModelProfile{Provider: tc.provider, Runtime: tc.runtime, ModelOptions: tc.options}); (err == nil) != tc.valid {
			t.Fatalf("%+v: %v", tc, err)
		}
	}
}

func TestMetadataMilliseconds(t *testing.T) {
	for _, value := range []any{float64(12), int64(0)} {
		if MetadataMilliseconds(map[string]any{"ms": value}, "ms") == nil {
			t.Fatal(value)
		}
	}
	for _, value := range []any{nil, "12", -1.0, int64(-1), 0.1, math.NaN(), math.Inf(1)} {
		if MetadataMilliseconds(map[string]any{"ms": value}, "ms") != nil {
			t.Fatal(value)
		}
	}
}
