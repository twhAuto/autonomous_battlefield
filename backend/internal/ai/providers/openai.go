// Adapts neutral model turns and tool schemas to OpenAI requests, then decodes tool calls
// and token usage.

package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai"
)

type OpenAI struct {
	APIKey, Model, Endpoint string
	// ReasoningEffort is sent as reasoning.effort; empty uses the model's default.
	ReasoningEffort string
	Client          *http.Client
}

type openAIOutput struct {
	Type      string `json:"type"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Content   []struct {
		Type string `json:"type"`
	} `json:"content"`
}

func (p *OpenAI) NextTurn(ctx context.Context, turn ai.ModelTurnRequest) (ai.ModelTurnResult, error) {
	tools, err := openAITools(turn.Tools)
	if err != nil {
		return ai.ModelTurnResult{}, err
	}
	payload := map[string]any{"model": p.Model, "instructions": turn.SystemPrompt, "input": openAIInput(turn.Messages), "tools": tools, "tool_choice": "auto", "max_output_tokens": turn.MaxTokens}
	if p.ReasoningEffort != "" {
		payload["reasoning"] = map[string]any{"effort": p.ReasoningEffort}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return ai.ModelTurnResult{}, err
	}
	endpoint := p.Endpoint
	if endpoint == "" {
		endpoint = "https://api.openai.com/v1/responses"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return ai.ModelTurnResult{}, err
	}
	request.Header.Set("Authorization", "Bearer "+p.APIKey)
	request.Header.Set("Content-Type", "application/json")
	var decoded struct {
		Model  string         `json:"model"`
		Output []openAIOutput `json:"output"`
		Usage  *struct {
			Input        int64 `json:"input_tokens"`
			Output       int64 `json:"output_tokens"`
			InputDetails struct {
				Cached *int64 `json:"cached_tokens"`
			} `json:"input_tokens_details"`
			OutputDetails struct {
				Reasoning *int64 `json:"reasoning_tokens"`
			} `json:"output_tokens_details"`
		} `json:"usage"`
	}
	if err := doJSON(p.Client, request, "OpenAI", &decoded); err != nil {
		return ai.ModelTurnResult{}, err
	}
	if decoded.Usage == nil {
		result, err := openAIResult(decoded.Output, 0, 0)
		result.Usage.TokensReported = false
		result.Model = decoded.Model
		return result, err
	}
	result, err := openAIResult(decoded.Output, decoded.Usage.Input, decoded.Usage.Output)
	result.Model = decoded.Model
	result.Usage.CachedInputTokens = decoded.Usage.InputDetails.Cached
	result.Usage.ReasoningOutputTokens = decoded.Usage.OutputDetails.Reasoning
	return result, err
}

// openAIInput sends the conversation as Responses input items: messages keep their
// roles, earlier calls become function_call items, and results become
// function_call_output items matched by call_id.
func openAIInput(messages []ai.Message) []map[string]any {
	items := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		switch message.Role {
		case "tool":
			items = append(items, map[string]any{"type": "function_call_output", "call_id": message.ToolCallID, "output": message.Content})
		case "assistant":
			if strings.TrimSpace(message.Content) != "" {
				items = append(items, map[string]any{"role": "assistant", "content": message.Content})
			}
			for _, call := range message.ToolCalls {
				items = append(items, map[string]any{"type": "function_call", "call_id": call.ID, "name": call.Name, "arguments": string(toolArguments(call))})
			}
		default:
			items = append(items, map[string]any{"role": "user", "content": message.Content})
		}
	}
	return items
}

func openAITools(definitions []ai.ToolDefinition) ([]map[string]any, error) {
	tools := make([]map[string]any, 0, len(definitions))
	for _, tool := range definitions {
		var schema any
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			return nil, err
		}
		// MCP tools may contain optional fields and JSON Schema keywords that do not
		// satisfy the Responses API's strict-schema subset. Keep the source schema
		// intact and let the MCP server perform authoritative input validation.
		tools = append(tools, map[string]any{"type": "function", "name": tool.Name, "description": tool.Description, "parameters": schema})
	}
	return tools, nil
}

func openAIResult(output []openAIOutput, inputTokens, outputTokens int64) (ai.ModelTurnResult, error) {
	result := ai.ModelTurnResult{Usage: ai.Usage{InputTokens: inputTokens, OutputTokens: outputTokens, Requests: 1, TokensReported: true}}
	for _, item := range output {
		for _, content := range item.Content {
			if content.Type == "refusal" {
				return result, ErrRefusal
			}
		}
		if item.Type == "function_call" {
			result.ToolCalls = append(result.ToolCalls, ai.ToolCall{ID: item.CallID, Name: item.Name, Arguments: json.RawMessage(item.Arguments)})
		}
	}
	return result, nil
}
