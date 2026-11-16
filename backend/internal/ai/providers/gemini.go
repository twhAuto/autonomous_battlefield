// Adapts neutral model turns and tool schemas to Gemini requests, then decodes tool calls
// and token usage.

package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
)

type Gemini struct {
	APIKey, Model, Endpoint string
	// ReasoningEffort is sent as thinkingConfig.thinkingLevel; empty uses the model's
	// default.
	ReasoningEffort string
	Client          *http.Client
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
			ThoughtSignature string `json:"thoughtSignature"`
		} `json:"parts"`
	} `json:"content"`
}

func (p *Gemini) NextTurn(ctx context.Context, turn ai.ModelTurnRequest) (ai.ModelTurnResult, error) {
	declarations, err := geminiToolDeclarations(turn.Tools)
	if err != nil {
		return ai.ModelTurnResult{}, err
	}
	generation := map[string]any{"maxOutputTokens": turn.MaxTokens}
	if p.ReasoningEffort != "" {
		generation["thinkingConfig"] = map[string]any{"thinkingLevel": p.ReasoningEffort}
	}
	body, err := json.Marshal(map[string]any{
		"systemInstruction": map[string]any{"parts": []any{map[string]any{"text": turn.SystemPrompt}}},
		"contents":          geminiContents(turn.Messages),
		"tools":             []any{map[string]any{"functionDeclarations": declarations}},
		"toolConfig":        map[string]any{"functionCallingConfig": map[string]any{"mode": "ANY"}},
		"generationConfig":  generation,
	})
	if err != nil {
		return ai.ModelTurnResult{}, err
	}
	endpoint := p.Endpoint
	if endpoint == "" {
		endpoint = "https://generativelanguage.googleapis.com/v1beta/models/" + url.PathEscape(p.Model) + ":generateContent"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return ai.ModelTurnResult{}, err
	}
	request.Header.Set("x-goog-api-key", p.APIKey)
	request.Header.Set("Content-Type", "application/json")
	var decoded struct {
		ModelVersion string            `json:"modelVersion"`
		Candidates   []geminiCandidate `json:"candidates"`
		Usage        *struct {
			Prompt     int64  `json:"promptTokenCount"`
			Candidates int64  `json:"candidatesTokenCount"`
			Cached     *int64 `json:"cachedContentTokenCount"`
			Thoughts   *int64 `json:"thoughtsTokenCount"`
		} `json:"usageMetadata"`
	}
	if err := doJSON(p.Client, request, "Gemini", &decoded); err != nil {
		return ai.ModelTurnResult{}, err
	}
	if decoded.Usage == nil {
		result, err := geminiResult(decoded.Candidates, 0, 0)
		result.Usage.TokensReported = false
		result.Model = decoded.ModelVersion
		return result, err
	}
	result, err := geminiResult(decoded.Candidates, decoded.Usage.Prompt, decoded.Usage.Candidates)
	result.Model = decoded.ModelVersion
	result.Usage.CachedInputTokens = decoded.Usage.Cached
	result.Usage.ReasoningOutputTokens = decoded.Usage.Thoughts
	// Gemini candidates exclude thoughts; normalize output to include reasoning.
	if decoded.Usage.Thoughts != nil {
		result.Usage.OutputTokens += *decoded.Usage.Thoughts
	}
	return result, err
}

// geminiContents sends the conversation as alternating user and model turns. Earlier
// calls become functionCall parts with their thought signatures, and their results
// become functionResponse parts grouped into the following user turn, in call order.
func geminiContents(messages []ai.Message) []map[string]any {
	var contents []map[string]any
	var responses []any
	flush := func() {
		if len(responses) > 0 {
			contents = append(contents, map[string]any{"role": "user", "parts": responses})
			responses = nil
		}
	}
	for _, message := range messages {
		switch message.Role {
		case "tool":
			responses = append(responses, map[string]any{"functionResponse": map[string]any{"name": message.Name, "response": geminiResponse(message.Content)}})
		case "assistant":
			flush()
			if parts := geminiModelParts(message); len(parts) > 0 {
				contents = append(contents, map[string]any{"role": "model", "parts": parts})
			}
		default:
			flush()
			contents = append(contents, map[string]any{"role": "user", "parts": []any{map[string]any{"text": message.Content}}})
		}
	}
	flush()
	return contents
}

func geminiModelParts(message ai.Message) []any {
	var parts []any
	if strings.TrimSpace(message.Content) != "" {
		parts = append(parts, map[string]any{"text": message.Content})
	}
	for _, call := range message.ToolCalls {
		var args map[string]any
		if json.Unmarshal(toolArguments(call), &args) != nil || args == nil {
			args = map[string]any{}
		}
		part := map[string]any{"functionCall": map[string]any{"name": call.Name, "args": args}}
		if call.Signature != "" {
			part["thoughtSignature"] = call.Signature
		}
		parts = append(parts, part)
	}
	return parts
}

// geminiResponse wraps a tool result as the JSON object functionResponse requires.
func geminiResponse(content string) map[string]any {
	var object map[string]any
	if json.Unmarshal([]byte(content), &object) == nil && object != nil {
		return object
	}
	return map[string]any{"result": content}
}

func geminiToolDeclarations(tools []ai.ToolDefinition) ([]map[string]any, error) {
	declarations := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		var schema any
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			return nil, err
		}
		// MCP exposes JSON Schema, which can contain fields outside Gemini's typed
		// OpenAPI Schema object. parametersJsonSchema accepts that schema without
		// lossy provider-specific rewriting.
		declarations = append(declarations, map[string]any{"name": tool.Name, "description": tool.Description, "parametersJsonSchema": schema})
	}
	return declarations, nil
}

func geminiResult(candidates []geminiCandidate, inputTokens, outputTokens int64) (ai.ModelTurnResult, error) {
	result := ai.ModelTurnResult{Usage: ai.Usage{InputTokens: inputTokens, OutputTokens: outputTokens, Requests: 1, TokensReported: true}}
	for _, candidate := range candidates {
		if candidate.FinishReason == "SAFETY" || candidate.FinishReason == "BLOCKLIST" || candidate.FinishReason == "PROHIBITED_CONTENT" {
			return result, ErrRefusal
		}
		for _, part := range candidate.Content.Parts {
			result.Text += part.Text
			if part.FunctionCall != nil {
				arguments, _ := json.Marshal(part.FunctionCall.Args)
				result.ToolCalls = append(result.ToolCalls, ai.ToolCall{Name: part.FunctionCall.Name, Arguments: arguments, Signature: part.ThoughtSignature})
			}
		}
	}
	return result, nil
}
