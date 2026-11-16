package ai

import (
	"fmt"
	"slices"
	"strings"
)

// ResolveModelSelection uses only the operator's allowlist. Empty input preserves
// legacy profile defaults; explicit options may select an explicit default effort.
// An option's alias resolves to the option's ID.
func ResolveModelSelection(p ModelProfile, model, effort string) (string, string, error) {
	if model == "" {
		model = ConfiguredModel(p)
	}
	model = CanonicalModel(p, model)
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
	if model == ConfiguredModel(p) && effort == "" {
		return model, "", nil
	}
	return "", "", fmt.Errorf("model or reasoning effort is not enabled")
}

// ConfiguredModel is the model a selection without a model identifier runs.
func ConfiguredModel(p ModelProfile) string {
	if p.Runtime == "cli" {
		return p.Model
	}
	return p.ModelLabel
}

// CanonicalModel returns the ID of the option model names by an alias, or model itself.
func CanonicalModel(p ModelProfile, model string) string {
	for _, option := range p.ModelOptions {
		if slices.Contains(option.Aliases, model) {
			return option.ID
		}
	}
	return model
}

func validModelIdentifier(id string) bool {
	return id != "" && strings.TrimSpace(id) == id && !strings.HasPrefix(id, "-")
}

func ValidateModelOptions(p ModelProfile) error {
	if err := validateModelIdentifiers(p); err != nil {
		return err
	}
	for _, option := range p.ModelOptions {
		efforts := map[string]bool{}
		for _, effort := range option.ReasoningEfforts {
			if !supportsReasoningEffort(p.Provider, effort) || efforts[effort] {
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

// validateModelIdentifiers requires distinct option IDs, and aliases that each name
// exactly one option and never another option's ID.
func validateModelIdentifiers(p ModelProfile) error {
	seen := map[string]bool{}
	for _, option := range p.ModelOptions {
		if (p.Runtime != "cli" && p.Runtime != "api") || !validModelIdentifier(option.ID) || seen[option.ID] {
			return fmt.Errorf("invalid model options for profile %q", p.ID)
		}
		seen[option.ID] = true
	}
	for _, option := range p.ModelOptions {
		for _, alias := range option.Aliases {
			if !validModelIdentifier(alias) || seen[alias] {
				return fmt.Errorf("invalid model aliases for profile %q", p.ID)
			}
			seen[alias] = true
		}
	}
	return nil
}

// HasModelOption reports whether model, or an alias of it, is one of the profile's operator-approved options.
func HasModelOption(p ModelProfile, model string) bool {
	model = CanonicalModel(p, model)
	return slices.ContainsFunc(p.ModelOptions, func(option ModelOption) bool { return option.ID == model })
}

func supportsReasoningEffort(provider, effort string) bool {
	switch provider {
	case "codex-cli":
		return slices.Contains([]string{"none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"}, effort)
	case "claude-cli", "anthropic":
		return slices.Contains([]string{"low", "medium", "high", "xhigh", "max"}, effort)
	case "openai":
		return slices.Contains([]string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}, effort)
	case "gemini":
		return slices.Contains([]string{"minimal", "low", "medium", "high"}, effort)
	default:
		return false
	}
}
