package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
)

type Gemini struct {
	APIKey, Model, Endpoint string
	Client                  *http.Client
}

type geminiCandidate struct {
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
}

func (p *Gemini) NextTurn(ctx context.Context, turn ai.ModelTurnRequest) (ai.ModelTurnResult, error) {
	declarations, err := geminiToolDeclarations(turn.Tools)
	if err != nil {
		return ai.ModelTurnResult{}, err
	}
	transcript := modelTranscript(turn)
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
	var decoded struct {
		Candidates []geminiCandidate `json:"candidates"`
		Usage      struct {
			Prompt     int64 `json:"promptTokenCount"`
			Candidates int64 `json:"candidatesTokenCount"`
		} `json:"usageMetadata"`
	}
	if err := doJSON(p.Client, request, "Gemini", &decoded); err != nil {
		return ai.ModelTurnResult{}, err
	}
	return geminiResult(decoded.Candidates, decoded.Usage.Prompt, decoded.Usage.Candidates)
}

func geminiToolDeclarations(tools []ai.ToolDefinition) ([]map[string]any, error) {
	declarations := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		var schema any
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			return nil, err
		}
		declarations = append(declarations, map[string]any{"name": tool.Name, "description": tool.Description, "parameters": schema})
	}
	return declarations, nil
}

func geminiResult(candidates []geminiCandidate, inputTokens, outputTokens int64) (ai.ModelTurnResult, error) {
	result := ai.ModelTurnResult{Usage: ai.Usage{InputTokens: inputTokens, OutputTokens: outputTokens, Requests: 1}}
	for _, candidate := range candidates {
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
