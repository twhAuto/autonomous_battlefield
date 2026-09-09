// Records rejected tool-input shapes while keeping submitted payloads out of operational
// logs.

package mcpserver

import (
	"context"
	"encoding/json"
	"github.com/twh09/autonomous_battlefield/backend/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// SDK schema failures occur before our typed tool handler. Log only fixed field
// names and the authenticated request id, never arguments or raw error text.
func (s *Server) logToolInputFailures(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
		if method != "tools/call" {
			return next(ctx, method, request)
		}
		params, ok := request.GetParams().(*mcp.CallToolParamsRaw)
		if !ok {
			return next(ctx, method, request)
		}
		requestID, _ := ctx.Value(requestIDKey{}).(string)
		if requestID != "" {
			return s.traceToolCall(ctx, method, request, params, requestID, next)
		}
		result, err := next(ctx, method, request)
		s.logSubmissionFailure(ctx, params, result, err)
		return result, err
	}
}

func (s *Server) traceToolCall(ctx context.Context, method string, request mcp.Request, params *mcp.CallToolParamsRaw, requestID string, next mcp.MethodHandler) (mcp.Result, error) {
	// Only allowlisted names reach telemetry; raw arguments never leave the application.
	name := params.Name
	switch name {
	case "get_decision_context", "get_unit_details", "submit_strategy_plan", "get_background_context", "submit_faction_background":
	default:
		name = "unknown"
	}
	ctx, span := telemetry.StartAI(telemetry.DecisionContext(ctx, requestID), name, "tool")
	defer span.End()
	result, err := next(ctx, method, request)
	failed := err != nil
	if value, ok := result.(*mcp.CallToolResult); ok {
		attributes, rejected := toolResultMetrics(value)
		span.SetAttributes(attributes...)
		failed = failed || rejected
	}
	telemetry.AIOutcome(span, failed, "tool_rejected")
	s.logSubmissionFailure(ctx, params, result, err)
	return result, err
}

func toolResultMetrics(value *mcp.CallToolResult) ([]attribute.KeyValue, bool) {
	data, _ := json.Marshal(value)
	structured, _ := json.Marshal(value.StructuredContent)
	var submission struct {
		Accepted *bool `json:"accepted"`
	}
	_ = json.Unmarshal(structured, &submission)
	failed := value.IsError || (submission.Accepted != nil && !*submission.Accepted)
	// Transport bytes include JSON escaping and envelopes. Text bytes describe
	// readable content, not provider-measured input tokens.
	textBytes := 0
	for _, content := range value.Content {
		if part, ok := content.(*mcp.TextContent); ok {
			textBytes += len(part.Text)
		}
	}
	structuredBytes := 0
	if value.StructuredContent != nil {
		structuredBytes = len(structured)
	}
	return []attribute.KeyValue{
		attribute.Int("battlefield.tool.response_bytes", len(data)),
		attribute.Int("battlefield.tool.text_bytes", textBytes),
		attribute.Int("battlefield.tool.structured_bytes", structuredBytes),
	}, failed
}

func (s *Server) logSubmissionFailure(ctx context.Context, params *mcp.CallToolParamsRaw, result mcp.Result, err error) {
	if params.Name != "submit_strategy_plan" {
		return
	}
	toolResult, _ := result.(*mcp.CallToolResult)
	if err == nil && (toolResult == nil || !toolResult.IsError) {
		return
	}
	requestID, _ := ctx.Value(requestIDKey{}).(string)
	missing, wrapped := submissionInputShape(params.Arguments)
	s.logger.WarnContext(ctx, "decision_tool_input_rejected", "request_id", requestID, "missing_fields", missing, "nested_plan", wrapped)
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
