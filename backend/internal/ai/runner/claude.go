package runner

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
)

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
	for _, name := range ai.SessionToolNames(session) {
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
	var event struct {
		Type    string            `json:"type"`
		Subtype string            `json:"subtype"`
		IsError bool              `json:"is_error"`
		Denials []json.RawMessage `json:"permission_denials"`
		Usage   struct {
			Input    *int64 `json:"input_tokens"`
			Output   *int64 `json:"output_tokens"`
			Read     *int64 `json:"cache_read_input_tokens"`
			Creation *int64 `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(output, &event) != nil || event.Type != "result" {
		result.ErrorCategory = "cli"
		return result, fmt.Errorf("Claude returned no terminal result")
	}
	u := event.Usage
	if u.Input != nil && u.Output != nil && *u.Input >= 0 && *u.Output >= 0 {
		result.Usage.InputTokens, result.Usage.OutputTokens = *u.Input, *u.Output
		result.Usage.TokensReported = true
		// Anthropic input_tokens excludes cache reads and writes; normalize to total input.
		for _, count := range []*int64{u.Read, u.Creation} {
			if count != nil && *count >= 0 {
				result.Usage.InputTokens += *count
			}
		}
		if u.Read != nil && *u.Read >= 0 {
			result.Usage.CachedInputTokens = u.Read
		}
	}
	if len(event.Denials) > 0 {
		result.ErrorCategory = "cli_permission"
		return result, fmt.Errorf("Claude denied a required tool action")
	}
	if event.IsError || event.Subtype != "success" {
		result.ErrorCategory = "cli"
		return result, fmt.Errorf("Claude returned an unsuccessful terminal result")
	}
	result.Accepted = true
	return result, nil
}
