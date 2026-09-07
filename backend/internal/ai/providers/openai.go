// Adapts neutral model turns and tool schemas to OpenAI requests, then decodes tool calls
// and token usage.

package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
)

type OpenAI struct {
	APIKey, Model, Endpoint string
	Client                  *http.Client
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
	transcript := modelTranscript(turn)
	body, err := json.Marshal(map[string]any{"model": p.Model, "input": transcript, "tools": tools, "tool_choice": "auto", "max_output_tokens": turn.MaxTokens})
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
		Output []openAIOutput `json:"output"`
		Usage  struct {
			Input  int64 `json:"input_tokens"`
			Output int64 `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := doJSON(p.Client, request, "OpenAI", &decoded); err != nil {
		return ai.ModelTurnResult{}, err
	}
	return openAIResult(decoded.Output, decoded.Usage.Input, decoded.Usage.Output)
}

func openAITools(definitions []ai.ToolDefinition) ([]map[string]any, error) {
	tools := make([]map[string]any, 0, len(definitions))
	for _, tool := range definitions {
		var schema any
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			return nil, err
		}
		tools = append(tools, map[string]any{"type": "function", "name": tool.Name, "description": tool.Description, "parameters": schema, "strict": true})
	}
	return tools, nil
}

func openAIResult(output []openAIOutput, inputTokens, outputTokens int64) (ai.ModelTurnResult, error) {
	result := ai.ModelTurnResult{Usage: ai.Usage{InputTokens: inputTokens, OutputTokens: outputTokens, Requests: 1}}
	for _, item := range output {
		for _, content := range item.Content {
			if content.Type == "refusal" {
				return ai.ModelTurnResult{}, ErrRefusal
			}
		}
		if item.Type == "function_call" {
			result.ToolCalls = append(result.ToolCalls, ai.ToolCall{ID: item.CallID, Name: item.Name, Arguments: json.RawMessage(item.Arguments)})
		}
	}
	return result, nil
}
