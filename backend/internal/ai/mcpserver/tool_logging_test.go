// Checks tool-schema failures are logged without exposing submitted payloads.

package mcpserver

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
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

func TestBoundedLogTextKeepsWholeRunes(t *testing.T) {
	if got := boundedLogText("short", 10); got != "short" {
		t.Fatalf("short text changed: %q", got)
	}
	if got := boundedLogText("abcé", 4); got != "abc…" {
		t.Fatalf("split a rune: %q", got)
	}
}
