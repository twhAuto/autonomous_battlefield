// Records rejected tool-input shapes while keeping submitted payloads out of operational
// logs.

package mcpserver

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
	"github.com/twh09/autonomous_battlefield/backend/internal/observability/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

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
	ctx, span := tracing.StartAI(tracing.DecisionContext(ctx, requestID), name, "tool")
	defer span.End()
	result, err := next(ctx, method, request)
	failed := err != nil
	if value, ok := result.(*mcp.CallToolResult); ok {
		attributes, rejected := toolResultMetrics(value)
		span.SetAttributes(attributes...)
		failed = failed || rejected
	}
	tracing.AIOutcome(span, failed, "tool_rejected")
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

// validationLogMessageBytes bounds validator-authored messages after submitted
// values are masked, so a rejection is diagnosable without logging submissions.
const validationLogMessageBytes = 240

// quotedValue matches a Go-quoted (%q) string, the only way validators
// interpolate submitted text.
var quotedValue = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)

// validationLogMessage masks every quoted submitted value, drops anything after
// an unbalanced quote, then bounds the remaining validator template.
func validationLogMessage(message string) string {
	const mask = `"…"`
	parts := strings.Split(quotedValue.ReplaceAllString(message, mask), mask)
	for i, part := range parts {
		if cut := strings.IndexByte(part, '"'); cut >= 0 {
			parts[i] = part[:cut]
			parts = parts[:i+1]
			break
		}
	}
	return boundedLogText(strings.Join(parts, mask), validationLogMessageBytes)
}

func boundedLogText(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "…"
}

// planList is how many entries a submitted plan list holds.
type planList struct {
	name  string
	count int
}

// planShape counts each plan list, so the logs and traces of a rejection show what a
// model sent (for example priorities alongside typed orders) without any submitted value.
func planShape(plan controller.PlanProposal) []planList {
	return []planList{
		{"spend_order", len(plan.SpendOrder)},
		{"construction_orders", listLength(plan.ConstructionOrders)},
		{"construction_priorities", len(plan.ConstructionPriorities)},
		{"production_orders", listLength(plan.ProductionOrders)},
		{"recruitment_orders", listLength(plan.RecruitmentOrders)},
		{"recruitment_priorities", len(plan.RecruitmentPriorities)},
		{"upgrade_orders", len(plan.UpgradeOrders)},
	}
}

func listLength[T any](list *[]T) int {
	if list == nil {
		return 0
	}
	return len(*list)
}

// recordSubmissionSpan records the outcome on the submit tool's span. Rejections add
// each issue's field and code, server-defined names that carry no submitted value, so
// traces show which rules a model broke.
func recordSubmissionSpan(span trace.Span, plan controller.PlanProposal, result ai.SubmissionResult) {
	attributes := []attribute.KeyValue{attribute.Bool("battlefield.submission.accepted", result.Accepted)}
	for _, list := range planShape(plan) {
		attributes = append(attributes, attribute.Int("battlefield.plan."+list.name, list.count))
	}
	if len(result.Errors) > 0 {
		fields, codes := make([]string, 0, len(result.Errors)), make([]string, 0, len(result.Errors))
		for _, issue := range result.Errors {
			fields, codes = append(fields, issue.Field), append(codes, issue.Code)
		}
		attributes = append(attributes, attribute.StringSlice("battlefield.validation.fields", fields), attribute.StringSlice("battlefield.validation.codes", codes))
	}
	span.SetAttributes(attributes...)
}
