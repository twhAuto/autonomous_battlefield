// Records rejected tool-input shapes while keeping submitted payloads out of operational
// logs.

package mcpserver

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// SDK schema failures occur before our typed tool handler. Log only fixed field
// names and the authenticated request id, never arguments or raw error text.
func (s *Server) logToolInputFailures(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
		result, err := next(ctx, method, request)
		if method != "tools/call" {
			return result, err
		}
		params, ok := request.GetParams().(*mcp.CallToolParamsRaw)
		if !ok || params.Name != "submit_strategy_plan" {
			return result, err
		}
		toolResult, _ := result.(*mcp.CallToolResult)
		if err == nil && (toolResult == nil || !toolResult.IsError) {
			return result, err
		}
		requestID, _ := ctx.Value(requestIDKey{}).(string)
		missing, wrapped := submissionInputShape(params.Arguments)
		s.logger.WarnContext(ctx, "decision_tool_input_rejected", "request_id", requestID, "missing_fields", missing, "nested_plan", wrapped)
		return result, err
	}
}

func submissionInputShape(payload json.RawMessage) ([]string, bool) {
	var arguments map[string]json.RawMessage
	_ = json.Unmarshal(payload, &arguments)
	var missing []string
	for _, field := range []string{"requestId", "idempotencyKey", "expectedVersion", "targetTick", "name", "rationale", "actionWeights", "reserveGold", "spendOrder", "constructionPriorities", "recruitmentPriorities"} {
		if _, exists := arguments[field]; !exists {
			missing = append(missing, field)
		}
	}
	_, wrapped := arguments["plan"]
	return missing, wrapped
}
