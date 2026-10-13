// Runs a bounded provider/tool loop over MCP, tracking usage and stopping on acceptance,
// failure, or lack of progress.

package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"go.opentelemetry.io/otel/attribute"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/agentview"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/prompts"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/providers"
)

type APIRunner struct {
	Provider  ai.TurnProvider
	Connector MCPConnector
}

func (r APIRunner) Run(ctx context.Context, session ai.DecisionSession) (ai.DecisionResult, error) {
	connector := r.Connector
	if connector == nil {
		connector = MCPClient{}
	}
	clientSession, err := connector.Connect(ctx, session.MCPURL, session.CapabilityToken)
	if err != nil {
		return ai.DecisionResult{ErrorCategory: "mcp_connect"}, err
	}
	defer clientSession.Close()
	clientSession = identityContextSession{DecisionToolSession: clientSession, identity: session}
	tools, err := clientSession.Tools(ctx)
	if err != nil {
		return ai.DecisionResult{ErrorCategory: "mcp_discovery"}, err
	}
	messages := []ai.Message{{Role: "user", Content: prompts.SessionDecision(session, nil)}}
	usage := ai.Usage{Scope: "api_calls", ModelCalls: new(int64)}
	for step := 0; step < session.Profile.MaxSteps; step++ {
		turnCtx, span := generationSpan(ctx, session, "api_call")
		messageJSON, _ := json.Marshal(messages)
		toolJSON, _ := json.Marshal(tools)
		span.SetAttributes(attribute.Int("battlefield.prompt.system_bytes", len(prompts.SessionSystem(session))), attribute.Int("battlefield.prompt.message_json_bytes", len(messageJSON)), attribute.Int("battlefield.prompt.tool_schema_json_bytes", len(toolJSON)))
		turn, err := r.Provider.NextTurn(turnCtx, ai.ModelTurnRequest{SystemPrompt: prompts.SessionSystem(session), Messages: messages, Tools: tools, MaxTokens: session.Profile.MaxOutputTokens})
		finishGeneration(span, turn.Usage, err != nil)
		*usage.ModelCalls++
		usage.TokensReported = usage.TokensReported || turn.Usage.TokensReported
		sumOptional(&usage.CachedInputTokens, turn.Usage.CachedInputTokens)
		sumOptional(&usage.ReasoningOutputTokens, turn.Usage.ReasoningOutputTokens)
		usage.InputTokens += turn.Usage.InputTokens
		usage.OutputTokens += turn.Usage.OutputTokens
		usage.Requests += max(1, turn.Usage.Requests)
		if err != nil {
			return ai.DecisionResult{ErrorCategory: providerErrorCategory(ctx, err), Usage: usage}, err
		}
		if len(turn.ToolCalls) == 0 {
			return ai.DecisionResult{ErrorCategory: "no_progress", Usage: usage}, fmt.Errorf("provider returned no tool call")
		}
		calls := withCallIDs(turn.ToolCalls, step)
		messages = append(messages, ai.Message{Role: "assistant", Content: turn.Text, ToolCalls: calls})
		accepted, updatedMessages, err := executeToolCalls(ctx, clientSession, calls, messages, prompts.SessionToolNames(session)...)
		if err != nil {
			return ai.DecisionResult{ErrorCategory: toolErrorCategory(err), Usage: usage}, err
		}
		messages = updatedMessages
		if accepted {
			return ai.DecisionResult{Accepted: true, Usage: usage}, nil
		}
	}
	return ai.DecisionResult{ErrorCategory: "step_limit", Usage: usage}, fmt.Errorf("provider exceeded %d steps", session.Profile.MaxSteps)
}

func providerErrorCategory(ctx context.Context, err error) string {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, providers.ErrRefusal) {
		return "refusal"
	}
	if errors.Is(err, providers.ErrRateLimit) {
		return "rate_limit"
	}
	return "provider"
}

var errUnauthorizedTool = errors.New("unauthorized tool")

type identityContextSession struct {
	DecisionToolSession
	identity ai.DecisionSession
}

func (s identityContextSession) Call(ctx context.Context, call ai.ToolCall) (json.RawMessage, bool, error) {
	payload, accepted, err := s.DecisionToolSession.Call(ctx, call)
	if err == nil && call.Name == "get_decision_context" {
		payload = agentview.SessionContextPayload(s.identity, payload)
	}
	return payload, accepted, err
}

func executeToolCalls(ctx context.Context, session DecisionToolSession, calls []ai.ToolCall, messages []ai.Message, allowed ...string) (bool, []ai.Message, error) {
	for _, call := range calls {
		if len(allowed) == 0 {
			allowed = []string{"get_decision_context", "submit_strategy_plan", "get_unit_details"}
		}
		if !slices.Contains(allowed, call.Name) {
			return false, messages, fmt.Errorf("%w: provider requested unsupported tool %q", errUnauthorizedTool, call.Name)
		}
		payload, accepted, err := session.Call(ctx, call)
		if err != nil {
			return false, messages, err
		}
		messages = append(messages, ai.Message{Role: "tool", ToolCallID: call.ID, Name: call.Name, Content: string(payload)})
		if accepted {
			return true, messages, nil
		}
	}
	return false, messages, nil
}

// Providers that do not identify calls get stable per-turn IDs, so every result in the
// conversation can be matched to the call it answers.
func withCallIDs(calls []ai.ToolCall, step int) []ai.ToolCall {
	identified := slices.Clone(calls)
	for i := range identified {
		if identified[i].ID == "" {
			identified[i].ID = fmt.Sprintf("call_%d_%d", step, i)
		}
	}
	return identified
}

func toolErrorCategory(err error) string {
	if errors.Is(err, errUnauthorizedTool) {
		return "unauthorized_tool"
	}
	return "mcp_tool"
}
