package runner

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
)

type codexEvent struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Error   struct {
		Message string `json:"message"`
	} `json:"error"`
	Usage *struct {
		Input     *int64 `json:"input_tokens"`
		Output    *int64 `json:"output_tokens"`
		Cached    *int64 `json:"cached_input_tokens"`
		Reasoning *int64 `json:"reasoning_output_tokens"`
	} `json:"usage"`
}

type codexTurn struct {
	completed, failed bool
	failure           string
}

// Codex JSONL turn.completed usage is the CLI turn aggregate, not a count
// of underlying model calls. Ignore item events (including private reasoning).
func codexResult(output []byte) (ai.DecisionResult, error) {
	result := ai.DecisionResult{Usage: ai.Usage{Scope: "cli_session", CLIInvocations: 1, Requests: 1}}
	turn := codexTurn{}
	for _, line := range strings.Split(string(output), "\n") {
		var event codexEvent
		if json.Unmarshal([]byte(line), &event) == nil {
			turn.apply(event, &result.Usage)
		}
	}
	if !turn.completed || turn.failed {
		if turn.failure != "" {
			var err error
			result.ErrorCategory, err = cliFailure("Codex", turn.failure)
			return result, err
		}
		result.ErrorCategory = "cli"
		return result, fmt.Errorf("Codex returned no successful terminal turn")
	}
	result.Accepted = true
	return result, nil
}

// apply records terminal state, the latest CLI error message and reported usage.
func (t *codexTurn) apply(event codexEvent, usage *ai.Usage) {
	switch event.Type {
	case "error":
		t.failure = event.Message
	case "turn.failed":
		t.failed = true
		if event.Error.Message != "" {
			t.failure = event.Error.Message
		}
	case "turn.completed":
		t.completed = true
		if event.Usage == nil || event.Usage.Input == nil || event.Usage.Output == nil || *event.Usage.Input < 0 || *event.Usage.Output < 0 {
			return
		}
		usage.InputTokens += *event.Usage.Input
		usage.OutputTokens += *event.Usage.Output
		usage.TokensReported = true
		sumOptional(&usage.CachedInputTokens, event.Usage.Cached)
		sumOptional(&usage.ReasoningOutputTokens, event.Usage.Reasoning)
	}
}
