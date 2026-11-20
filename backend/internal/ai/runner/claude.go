package runner

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai/prompts"
)

type claudeUsage struct {
	Input    *int64 `json:"input_tokens"`
	Output   *int64 `json:"output_tokens"`
	Read     *int64 `json:"cache_read_input_tokens"`
	Creation *int64 `json:"cache_creation_input_tokens"`
}

type claudeModelUsage struct {
	Input  int64 `json:"inputTokens"`
	Output int64 `json:"outputTokens"`
}

type claudeEvent struct {
	Type    string            `json:"type"`
	Subtype string            `json:"subtype"`
	IsError bool              `json:"is_error"`
	Result  string            `json:"result"`
	Denials []json.RawMessage `json:"permission_denials"`
	Usage   claudeUsage       `json:"usage"`
	// ModelUsage is keyed by the full ID of each model the session ran, which shows
	// what a model alias resolved to.
	ModelUsage map[string]claudeModelUsage `json:"modelUsage"`
}

// The MCP capability lives in a private temporary file, never process arguments.
func claudeArguments(directory, prompt, model string, session ai.DecisionSession) ([]string, error) {
	config, err := json.Marshal(map[string]any{"mcpServers": map[string]any{"battlefield": map[string]any{
		"type": "http", "url": session.MCPURL, "headers": map[string]string{"Authorization": "Bearer " + session.CapabilityToken},
	}}})
	if err != nil {
		return nil, err
	}
	path := filepath.Join(directory, "claude-mcp.json")
	if err := os.WriteFile(path, config, 0o600); err != nil {
		return nil, err
	}
	allowed := []string{}
	for _, name := range prompts.SessionToolNames(session) {
		allowed = append(allowed, "mcp__battlefield__"+name)
	}
	return withModel([]string{"--print", prompt, "--restricted", "--tools", "", "--allowedTools", strings.Join(allowed, ","),
		"--permission-mode", "dontAsk", "--strict-mcp-config", "--mcp-config", path,
		"--setting-sources", "", "--settings", `{"disableAllHooks":true}`, "--disable-slash-commands",
		"--no-session-persistence", "--output-format", "json"}, model), nil
}

// Ignore assistant text and reasoning. Only a successful terminal result counts;
// the worker independently verifies that the scoped MCP submission was accepted.
func claudeResult(output []byte) (ai.DecisionResult, error) {
	result := ai.DecisionResult{Usage: ai.Usage{Scope: "cli_session", CLIInvocations: 1, Requests: 1}}
	var event claudeEvent
	if json.Unmarshal(output, &event) != nil || event.Type != "result" {
		result.ErrorCategory = "cli"
		return result, fmt.Errorf("Claude returned no terminal result")
	}
	applyClaudeUsage(&result.Usage, event.Usage)
	result.RecordResolvedModel(claudeMainModel(event.ModelUsage))
	if len(event.Denials) > 0 {
		result.ErrorCategory = "cli_permission"
		return result, fmt.Errorf("Claude denied a required tool action")
	}
	// An error result's text is the CLI's error message, never the model's answer.
	if event.IsError && event.Result != "" {
		var err error
		result.ErrorCategory, err = cliFailure("Claude", event.Result)
		return result, err
	}
	if event.IsError || event.Subtype != "success" {
		result.ErrorCategory = "cli"
		return result, fmt.Errorf("Claude returned an unsuccessful terminal result")
	}
	result.Accepted = true
	return result, nil
}

// claudeMainModel picks the model that produced the most output, then read the most
// input, so a smaller model the CLI used for an internal step does not hide the
// selected one. Ties go to the first ID in sort order.
func claudeMainModel(usage map[string]claudeModelUsage) string {
	main := ""
	for _, model := range slices.Sorted(maps.Keys(usage)) {
		current, best := usage[model], usage[main]
		if main == "" || current.Output > best.Output || (current.Output == best.Output && current.Input > best.Input) {
			main = model
		}
	}
	return main
}

func applyClaudeUsage(result *ai.Usage, usage claudeUsage) {
	if usage.Input == nil || usage.Output == nil || *usage.Input < 0 || *usage.Output < 0 {
		return
	}
	result.InputTokens, result.OutputTokens = *usage.Input, *usage.Output
	result.TokensReported = true
	// Anthropic input_tokens excludes cache reads and writes; normalize to total input.
	for _, count := range []*int64{usage.Read, usage.Creation} {
		if count != nil && *count >= 0 {
			result.InputTokens += *count
		}
	}
	if usage.Read != nil && *usage.Read >= 0 {
		result.CachedInputTokens = usage.Read
	}
}
