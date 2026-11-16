package ai

import "regexp"

// ResolvedModelKey is the decision metadata key for the model the provider reports
// having run. It can differ from the selected model when the selection is an alias,
// such as a Claude Code model alias, whose meaning depends on the installed CLI.
const ResolvedModelKey = "resolvedModel"

var resolvedModelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@/-]{0,127}$`)

// ResolvedModel exposes only a well-formed provider model identifier, never arbitrary
// worker-reported metadata.
func ResolvedModel(metadata map[string]any) string {
	value, _ := metadata[ResolvedModelKey].(string)
	if resolvedModelPattern.MatchString(value) {
		return value
	}
	return ""
}

// RecordResolvedModel stores a provider-reported model in the result metadata. An
// empty or malformed identifier records nothing.
func (r *DecisionResult) RecordResolvedModel(model string) {
	if !resolvedModelPattern.MatchString(model) {
		return
	}
	if r.ProviderData == nil {
		r.ProviderData = map[string]any{}
	}
	r.ProviderData[ResolvedModelKey] = model
}
