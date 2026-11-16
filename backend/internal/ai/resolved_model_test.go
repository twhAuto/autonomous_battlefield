package ai

import (
	"strings"
	"testing"
)

func TestResolvedModelExposesOnlyWellFormedIdentifiers(t *testing.T) {
	for value, want := range map[any]string{
		"claude-opus-5-5":          "claude-opus-5-5",
		"gemini-3.8-flash-001":     "gemini-3.8-flash-001",
		"models/gemini-4-hydrogen": "models/gemini-4-hydrogen",
		"gpt-6-luna-2026-10-01":    "gpt-6-luna-2026-10-01",
		"":                         "",
		"claude opus":              "",
		"<script>":                 "",
		"-leading-dash":            "",
		strings.Repeat("a", 129):   "",
		42:                         "",
	} {
		if got := ResolvedModel(map[string]any{ResolvedModelKey: value}); got != want {
			t.Errorf("ResolvedModel(%v) = %q, want %q", value, got, want)
		}
	}
	if ResolvedModel(nil) != "" {
		t.Error("nil metadata reported a model")
	}
}

func TestRecordResolvedModelKeepsExistingMetadata(t *testing.T) {
	var result DecisionResult
	result.RecordResolvedModel("")
	result.RecordResolvedModel("not a model")
	if result.ProviderData != nil {
		t.Fatalf("invalid models created metadata: %v", result.ProviderData)
	}
	result.ProviderData = map[string]any{"contextDelivery": "prefetch"}
	result.RecordResolvedModel("claude-sonnet-5-5")
	if result.ProviderData["contextDelivery"] != "prefetch" || ResolvedModel(result.ProviderData) != "claude-sonnet-5-5" {
		t.Fatalf("metadata=%v", result.ProviderData)
	}
	var fresh DecisionResult
	fresh.RecordResolvedModel("claude-haiku-5-5")
	if ResolvedModel(fresh.ProviderData) != "claude-haiku-5-5" {
		t.Fatalf("metadata=%v", fresh.ProviderData)
	}
}
