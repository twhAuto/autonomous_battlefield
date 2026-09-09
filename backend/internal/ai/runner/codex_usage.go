package runner

import (
	"encoding/json"
	"fmt"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"strings"
)

// Codex JSONL turn.completed usage is the CLI turn aggregate, not a count
// of underlying model calls. Ignore item events (including private reasoning).
func codexResult(output []byte) (ai.DecisionResult, error) {
	result := ai.DecisionResult{Usage: ai.Usage{Scope: "cli_session", CLIInvocations: 1, Requests: 1}}
	completed, failed := false, false
	for _, line := range strings.Split(string(output), "\n") {
		var event struct {
			Type  string `json:"type"`
			Usage *struct {
				Input     *int64 `json:"input_tokens"`
				Output    *int64 `json:"output_tokens"`
				Cached    *int64 `json:"cached_input_tokens"`
				Reasoning *int64 `json:"reasoning_output_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal([]byte(line), &event) != nil {
			continue
		}
		switch event.Type {
		case "turn.failed":
			failed = true
		case "turn.completed":
			completed = true
			if event.Usage == nil || event.Usage.Input == nil || event.Usage.Output == nil {
				continue
			}
			if *event.Usage.Input < 0 || *event.Usage.Output < 0 {
				continue
			}
			result.Usage.InputTokens += *event.Usage.Input
			result.Usage.OutputTokens += *event.Usage.Output
			result.Usage.TokensReported = true
			sumOptional(&result.Usage.CachedInputTokens, event.Usage.Cached)
			sumOptional(&result.Usage.ReasoningOutputTokens, event.Usage.Reasoning)
		}
	}
	if !completed || failed {
		result.ErrorCategory = "cli"
		return result, fmt.Errorf("Codex returned no successful terminal turn")
	}
	result.Accepted = true
	return result, nil
}
