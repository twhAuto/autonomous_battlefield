package runner

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
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
	tools, err := clientSession.Tools(ctx)
	if err != nil {
		return ai.DecisionResult{ErrorCategory: "mcp_discovery"}, err
	}
	messages := []ai.Message{{Role: "user", Content: fmt.Sprintf("Decide request %s. First call get_decision_context, then call submit_strategy_plan. Include a concise 1-3 sentence rationale based on visible facts, not private chain-of-thought. Do not invent state or omit submission.", session.RequestID)}}
	usage := ai.Usage{}
	for step := 0; step < session.Profile.MaxSteps; step++ {
		turn, err := r.Provider.NextTurn(ctx, ai.ModelTurnRequest{SystemPrompt: ai.SystemPrompt, Messages: messages, Tools: tools, MaxTokens: session.Profile.MaxOutputTokens})
		if err != nil {
			return ai.DecisionResult{ErrorCategory: providerErrorCategory(ctx, err), Usage: usage}, err
		}
		usage.InputTokens += turn.Usage.InputTokens
		usage.OutputTokens += turn.Usage.OutputTokens
		usage.Requests += max(1, turn.Usage.Requests)
		if len(turn.ToolCalls) == 0 {
			return ai.DecisionResult{ErrorCategory: "no_progress", Usage: usage}, fmt.Errorf("provider returned no tool call")
		}
		accepted, updatedMessages, err := executeToolCalls(ctx, clientSession, turn.ToolCalls, messages)
		if err != nil {
			return ai.DecisionResult{ErrorCategory: toolErrorCategory(err), Usage: usage}, err
		}
		messages = updatedMessages
		if accepted {
			return ai.DecisionResult{Accepted: true, Usage: usage}, nil
		}
		if strings.TrimSpace(turn.Text) != "" {
			messages = append(messages, ai.Message{Role: "assistant", Content: turn.Text})
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

func executeToolCalls(ctx context.Context, session DecisionToolSession, calls []ai.ToolCall, messages []ai.Message) (bool, []ai.Message, error) {
	for _, call := range calls {
		if call.Name != "get_decision_context" && call.Name != "submit_strategy_plan" {
			return false, messages, fmt.Errorf("%w: provider requested unsupported tool %q", errUnauthorizedTool, call.Name)
		}
		payload, accepted, err := session.Call(ctx, call)
		if err != nil {
			return false, messages, err
		}
		messages = append(messages, ai.Message{Role: "assistant", Content: "tool call " + call.Name + ": " + string(call.Arguments)}, ai.Message{Role: "tool", Content: string(payload)})
		if accepted {
			return true, messages, nil
		}
	}
	return false, messages, nil
}

func toolErrorCategory(err error) string {
	if errors.Is(err, errUnauthorizedTool) {
		return "unauthorized_tool"
	}
	return "mcp_tool"
}
