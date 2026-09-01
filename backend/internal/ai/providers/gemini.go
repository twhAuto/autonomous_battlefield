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

type Gemini struct {
	APIKey, Model, Endpoint string
	Client                  *http.Client
}

func (p *Gemini) NextTurn(ctx context.Context, turn ai.ModelTurnRequest) (ai.ModelTurnResult, error) {
	declarations := make([]map[string]any, 0, len(turn.Tools))
	for _, tool := range turn.Tools {
		var schema any
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			return ai.ModelTurnResult{}, err
		}
		declarations = append(declarations, map[string]any{"name": tool.Name, "description": tool.Description, "parameters": schema})
	}
	transcript := turn.SystemPrompt + "\n"
	for _, message := range turn.Messages {
		transcript += strings.ToUpper(message.Role) + ": " + message.Content + "\n"
	}
	body, err := json.Marshal(map[string]any{"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": transcript}}}}, "tools": []any{map[string]any{"functionDeclarations": declarations}}, "generationConfig": map[string]any{"maxOutputTokens": turn.MaxTokens}})
	if err != nil {
		return ai.ModelTurnResult{}, err
	}
	endpoint := p.Endpoint
	if endpoint == "" {
		endpoint = "https://generativelanguage.googleapis.com/v1beta/models/" + p.Model + ":generateContent"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return ai.ModelTurnResult{}, err
	}
	request.Header.Set("x-goog-api-key", p.APIKey)
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
		return ai.ModelTurnResult{}, fmt.Errorf("Gemini response status %d", response.StatusCode)
	}
	var decoded struct {
		Candidates []struct {
			FinishReason string `json:"finishReason"`
			Content      struct {
				Parts []struct {
					Text         string `json:"text"`
					FunctionCall *struct {
						Name string         `json:"name"`
						Args map[string]any `json:"args"`
					} `json:"functionCall"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		Usage struct {
			Prompt     int64 `json:"promptTokenCount"`
			Candidates int64 `json:"candidatesTokenCount"`
		} `json:"usageMetadata"`
	}
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return ai.ModelTurnResult{}, err
	}
	result := ai.ModelTurnResult{Usage: ai.Usage{InputTokens: decoded.Usage.Prompt, OutputTokens: decoded.Usage.Candidates, Requests: 1}}
	for _, candidate := range decoded.Candidates {
		if candidate.FinishReason == "SAFETY" || candidate.FinishReason == "BLOCKLIST" || candidate.FinishReason == "PROHIBITED_CONTENT" {
			return ai.ModelTurnResult{}, ErrRefusal
		}
		for _, part := range candidate.Content.Parts {
			result.Text += part.Text
			if part.FunctionCall != nil {
				arguments, _ := json.Marshal(part.FunctionCall.Args)
				result.ToolCalls = append(result.ToolCalls, ai.ToolCall{Name: part.FunctionCall.Name, Arguments: arguments})
			}
		}
	}
	return result, nil
}
