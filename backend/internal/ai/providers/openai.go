package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
)

type OpenAI struct {
	APIKey, Model, Endpoint string
	Client                  *http.Client
}

func (p *OpenAI) NextTurn(ctx context.Context, turn ai.ModelTurnRequest) (ai.ModelTurnResult, error) {
	tools := make([]map[string]any, 0, len(turn.Tools))
	for _, tool := range turn.Tools {
		var schema any
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			return ai.ModelTurnResult{}, err
		}
		tools = append(tools, map[string]any{"type": "function", "name": tool.Name, "description": tool.Description, "parameters": schema, "strict": true})
	}
	transcript := turn.SystemPrompt + "\n"
	for _, message := range turn.Messages {
		transcript += strings.ToUpper(message.Role) + ": " + message.Content + "\n"
	}
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
	client := p.Client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return ai.ModelTurnResult{}, err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return ai.ModelTurnResult{}, fmt.Errorf("OpenAI response status %d", response.StatusCode)
	}
	var decoded struct {
		Output []struct {
			Type      string `json:"type"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
			Content   []struct {
				Type string `json:"type"`
			} `json:"content"`
		} `json:"output"`
		Usage struct {
			Input  int64 `json:"input_tokens"`
			Output int64 `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return ai.ModelTurnResult{}, err
	}
	result := ai.ModelTurnResult{Usage: ai.Usage{InputTokens: decoded.Usage.Input, OutputTokens: decoded.Usage.Output, Requests: 1}}
	for _, item := range decoded.Output {
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
