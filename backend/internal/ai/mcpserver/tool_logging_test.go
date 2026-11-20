// Checks tool-schema failures are logged without exposing submitted payloads.

package mcpserver

import (
	"bytes"
	"context"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/game/controller"
)

func TestSubmissionSchemaFailuresAreLoggedWithoutPayload(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdk.NewTracerProvider(sdk.WithSpanProcessor(recorder))
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { otel.SetTracerProvider(old); _ = provider.Shutdown(context.Background()) })
	repository := &fakeRepository{record: ai.DecisionRecord{ID: "authorized-request"}}
	var logs bytes.Buffer
	server := NewWithLogger(repository, "secret", nil, slog.New(slog.NewJSONHandler(&logs, nil)))
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	ctx := context.WithValue(context.Background(), requestIDKey{}, "authorized-request")
	serverSession, err := server.mcpServer().Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "submit_strategy_plan", Arguments: map[string]any{"requestId": "untrusted-secret-id", "plan": map[string]any{"rationale": "private-payload-marker", "actionWeights": map[string]int{"attack": 1000}}}})
	if err == nil && (result == nil || !result.IsError) {
		t.Fatal("malformed arguments accepted")
	}
	spans := recorder.Ended()
	if len(spans) != 1 || spans[0].Name() != "submit_strategy_plan" || spans[0].Status().Code != codes.Error {
		t.Fatal("rejected tool trace missing", spans)
	}
	if repository.submitted.RequestID != "" {
		t.Fatal("invalid input reached plan persistence")
	}
	for _, want := range []string{"decision_tool_input_rejected", "authorized-request", "idempotencyKey", "actionWeights", `"nested_plan":true`} {
		if !strings.Contains(logs.String(), want) {
			t.Fatalf("missing safe diagnostic %s", want)
		}
	}
	for _, secret := range []string{"untrusted-secret-id", "private-payload-marker"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatal("raw input leaked to logs")
		}
	}
}

func TestSubmissionSpanRecordsRejectedRulesAndPlanShape(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdk.NewTracerProvider(sdk.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	_, span := provider.Tracer("test").Start(context.Background(), "submit_strategy_plan")
	orders := []controller.ConstructionOrder{{BuildingType: "defense_tower"}}
	plan := controller.PlanProposal{SpendOrder: []string{"construction"}, ConstructionOrders: &orders, ConstructionPriorities: []controller.Coordinate{{X: 1, Y: 1}}}
	recordSubmissionSpan(span, plan, ai.SubmissionResult{Errors: []ai.ValidationIssue{{Field: "plan", Code: "invalid", Message: "private-detail"}, {Field: "plan.spendOrder", Code: "unsupported"}}})
	span.End()
	attributes := map[attribute.Key]attribute.Value{}
	for _, kv := range recorder.Ended()[0].Attributes() {
		attributes[kv.Key] = kv.Value
	}
	if attributes["battlefield.submission.accepted"].AsBool() || attributes["battlefield.plan.construction_orders"].AsInt64() != 1 || attributes["battlefield.plan.construction_priorities"].AsInt64() != 1 ||
		!slices.Equal(attributes["battlefield.validation.fields"].AsStringSlice(), []string{"plan", "plan.spendOrder"}) ||
		!slices.Equal(attributes["battlefield.validation.codes"].AsStringSlice(), []string{"invalid", "unsupported"}) {
		t.Fatalf("attributes = %v", attributes)
	}
	for _, kv := range recorder.Ended()[0].Attributes() {
		if strings.Contains(kv.Value.Emit(), "private-detail") || strings.Contains(kv.Value.Emit(), "defense_tower") {
			t.Fatal("span recorded a submitted value")
		}
	}
}

func TestBoundedLogTextKeepsWholeRunes(t *testing.T) {
	if got := boundedLogText("short", 10); got != "short" {
		t.Fatalf("short text changed: %q", got)
	}
	if got := boundedLogText("abcé", 4); got != "abc…" {
		t.Fatalf("split a rune: %q", got)
	}
}

func TestValidationLogMessageMasksSubmittedValues(t *testing.T) {
	for message, want := range map[string]string{
		`upgrade "plating_3" requires "plating_2" owned or ordered earlier`: `upgrade "…" requires "…" owned or ordered earlier`,
		`construction "a\"b" at (1,1) is not legal`:                         `construction "…" at (1,1) is not legal`,
		`rationale exceeds 600 characters`:                                  `rationale exceeds 600 characters`,
		`unit "ok" then "unterminated secret`:                               `unit "…" then `,
		`"secret`:                                                           ``,
	} {
		if got := validationLogMessage(message); got != want {
			t.Errorf("validationLogMessage(%q) = %q, want %q", message, got, want)
		}
	}
	long := strings.Repeat("x", 300) + ` "secret"`
	if got := validationLogMessage(long); strings.Contains(got, "secret") || len(got) > validationLogMessageBytes+len("…") {
		t.Fatalf("unbounded or leaking message %q", got)
	}
}
