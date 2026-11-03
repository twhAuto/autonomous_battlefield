// Adapts neutral model turns and tool schemas to Claude Messages API requests through the
// official SDK, then decodes tool calls, token usage and the turn's replayable content.

package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/observability/tracing"
)

type Anthropic struct {
	APIKey, Model string
	// ReasoningEffort is sent as output_config.effort; empty uses the model's default.
	ReasoningEffort string
	Client          *http.Client
}

func (p *Anthropic) NextTurn(ctx context.Context, turn ai.ModelTurnRequest) (ai.ModelTurnResult, error) {
	tools, err := anthropicTools(turn.Tools)
	if err != nil {
		return ai.ModelTurnResult{}, err
	}
	messages, err := anthropicMessages(turn.Messages)
	if err != nil {
		return ai.ModelTurnResult{}, err
	}
	// Current Claude models reject a forced tool_choice, so the default auto choice
	// applies and the prompt requires the tool call. Thinking keeps each model's
	// default, which is adaptive on every offered model.
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(p.Model),
		MaxTokens: int64(turn.MaxTokens),
		System:    []anthropic.TextBlockParam{{Text: turn.SystemPrompt}},
		Messages:  messages,
		Tools:     tools,
	}
	if p.ReasoningEffort != "" {
		params.OutputConfig = anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffort(p.ReasoningEffort)}
	}
	client := p.client()
	response, err := client.Messages.New(ctx, params)
	if err != nil {
		return ai.ModelTurnResult{}, anthropicError(err)
	}
	return anthropicResult(response)
}

// client reads nothing from the process environment: the profile names the
// credential, and the shared agent loop owns retries and the decision deadline.
func (p *Anthropic) client() anthropic.Client {
	httpClient := p.Client
	if httpClient == nil {
		httpClient = tracing.HTTPClient(nil)
	}
	return anthropic.NewClient(
		option.WithoutEnvironmentDefaults(),
		option.WithAPIKey(p.APIKey),
		option.WithHTTPClient(httpClient),
		option.WithMaxRetries(0),
		option.WithMiddleware(limitResponse),
	)
}

// limitResponse bounds the body the SDK reads to the limit doJSON applies to the
// other adapters.
func limitResponse(request *http.Request, next option.MiddlewareNext) (*http.Response, error) {
	response, err := next(request)
	if err == nil {
		response.Body = http.MaxBytesReader(nil, response.Body, maxResponseBytes)
	}
	return response, err
}

// anthropicError keeps an API error's status and machine-readable type but drops the
// SDK's message, which embeds the response body.
func anthropicError(err error) error {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return fmt.Errorf("Anthropic response exceeds %d bytes", maxResponseBytes)
	}
	var apiErr *anthropic.Error
	if !errors.As(err, &apiErr) {
		return err
	}
	kind := errorKind(strings.NewReader(apiErr.RawJSON()))
	if apiErr.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("%w: Anthropic response status %d%s", ErrRateLimit, apiErr.StatusCode, kind)
	}
	return fmt.Errorf("Anthropic response status %d%s", apiErr.StatusCode, kind)
}

// anthropicMessages sends the conversation as user and assistant turns. Results of
// one turn's tool calls are grouped into the following user turn, in call order.
func anthropicMessages(messages []ai.Message) ([]anthropic.MessageParam, error) {
	var params []anthropic.MessageParam
	var results []anthropic.ContentBlockParamUnion
	flush := func() {
		if len(results) > 0 {
			params = append(params, anthropic.NewUserMessage(results...))
			results = nil
		}
	}
	for _, message := range messages {
		switch message.Role {
		case "tool":
			results = append(results, anthropic.NewToolResultBlock(message.ToolCallID, message.Content, false))
		case "assistant":
			flush()
			blocks, err := anthropicAssistantBlocks(message)
			if err != nil {
				return nil, err
			}
			params = append(params, anthropic.NewAssistantMessage(blocks...))
		default:
			flush()
			params = append(params, anthropic.NewUserMessage(anthropic.NewTextBlock(message.Content)))
		}
	}
	flush()
	return params, nil
}

// anthropicAssistantBlocks repeats a Claude turn's own content unchanged, because a
// later request must carry its thinking blocks exactly as returned. A turn without
// that content is rebuilt from its text and tool calls.
func anthropicAssistantBlocks(message ai.Message) ([]anthropic.ContentBlockParamUnion, error) {
	var blocks []anthropic.ContentBlockParamUnion
	if len(message.ProviderContent) > 0 {
		err := json.Unmarshal(message.ProviderContent, &blocks)
		return blocks, err
	}
	if strings.TrimSpace(message.Content) != "" {
		blocks = append(blocks, anthropic.NewTextBlock(message.Content))
	}
	for _, call := range message.ToolCalls {
		blocks = append(blocks, anthropic.NewToolUseBlock(call.ID, toolArguments(call), call.Name))
	}
	return blocks, nil
}

func anthropicTools(definitions []ai.ToolDefinition) ([]anthropic.ToolUnionParam, error) {
	tools := make([]anthropic.ToolUnionParam, 0, len(definitions))
	for _, definition := range definitions {
		var schema map[string]any
		if err := json.Unmarshal(definition.InputSchema, &schema); err != nil {
			return nil, err
		}
		// Keep the MCP JSON Schema intact, as for the other providers; the MCP server
		// performs authoritative input validation.
		input := anthropic.ToolInputSchemaParam{Properties: schema["properties"], ExtraFields: map[string]any{}}
		for key, value := range schema {
			if key != "type" && key != "properties" {
				input.ExtraFields[key] = value
			}
		}
		tool := anthropic.ToolParam{Name: definition.Name, Description: anthropic.String(definition.Description), InputSchema: input}
		tools = append(tools, anthropic.ToolUnionParam{OfTool: &tool})
	}
	return tools, nil
}

func anthropicResult(response *anthropic.Message) (ai.ModelTurnResult, error) {
	result := ai.ModelTurnResult{Usage: anthropicUsage(response)}
	if response.StopReason == anthropic.StopReasonRefusal {
		return result, ErrRefusal
	}
	blocks := make([]anthropic.ContentBlockParamUnion, 0, len(response.Content))
	for _, block := range response.Content {
		blocks = append(blocks, block.ToParam())
		switch variant := block.AsAny().(type) {
		case anthropic.TextBlock:
			result.Text += variant.Text
		case anthropic.ToolUseBlock:
			result.ToolCalls = append(result.ToolCalls, ai.ToolCall{ID: variant.ID, Name: variant.Name, Arguments: json.RawMessage(variant.JSON.Input.Raw())})
		}
	}
	content, err := json.Marshal(blocks)
	result.ProviderContent = content
	return result, err
}

func anthropicUsage(response *anthropic.Message) ai.Usage {
	if !response.JSON.Usage.Valid() {
		return ai.Usage{Requests: 1}
	}
	usage := response.Usage
	// Anthropic input_tokens excludes cache reads and writes; normalize to total input.
	result := ai.Usage{InputTokens: usage.InputTokens + usage.CacheReadInputTokens + usage.CacheCreationInputTokens, OutputTokens: usage.OutputTokens, Requests: 1, TokensReported: true}
	if usage.JSON.CacheReadInputTokens.Valid() {
		result.CachedInputTokens = &usage.CacheReadInputTokens
	}
	if usage.OutputTokensDetails.JSON.ThinkingTokens.Valid() {
		result.ReasoningOutputTokens = &usage.OutputTokensDetails.ThinkingTokens
	}
	return result
}
