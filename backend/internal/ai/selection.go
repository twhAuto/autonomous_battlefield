package ai

import (
	"fmt"
	"slices"
	"strings"
)

// ResolveModelSelection uses only the operator's allowlist. Empty input preserves
// legacy profile defaults; explicit options may select an explicit default effort.
func ResolveModelSelection(p ModelProfile, model, effort string) (string, string, error) {
	if model == "" {
		model = p.ModelLabel
		if p.Runtime == "cli" {
			model = p.Model
		}
	}
	for _, option := range p.ModelOptions {
		if option.ID != model {
			continue
		}
		if effort == "" {
			effort = option.DefaultReasoningEffort
		}
		if effort == "" || slices.Contains(option.ReasoningEfforts, effort) {
			return model, effort, nil
		}
		return "", "", fmt.Errorf("unsupported reasoning effort for model")
	}
	fallback := p.ModelLabel
	if p.Runtime == "cli" {
		fallback = p.Model
	}
	if model == fallback && effort == "" {
		return model, "", nil
	}
	return "", "", fmt.Errorf("model or reasoning effort is not enabled")
}

func ValidateModelOptions(p ModelProfile) error {
	seen := map[string]bool{}
	for _, option := range p.ModelOptions {
		if p.Runtime != "cli" || option.ID == "" || strings.TrimSpace(option.ID) != option.ID || strings.HasPrefix(option.ID, "-") || seen[option.ID] {
			return fmt.Errorf("invalid model options for profile %q", p.ID)
		}
		seen[option.ID] = true
		efforts := map[string]bool{}
		for _, effort := range option.ReasoningEfforts {
			supported := p.Provider == "codex-cli" && slices.Contains([]string{"none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"}, effort) || p.Provider == "claude-cli" && slices.Contains([]string{"low", "medium", "high", "xhigh", "max"}, effort)
			if !supported || efforts[effort] {
				return fmt.Errorf("invalid reasoning efforts for profile %q", p.ID)
			}
			efforts[effort] = true
		}
		if option.DefaultReasoningEffort != "" && !efforts[option.DefaultReasoningEffort] {
			return fmt.Errorf("default reasoning effort is not allowed for profile %q", p.ID)
		}
	}
	return nil
}
