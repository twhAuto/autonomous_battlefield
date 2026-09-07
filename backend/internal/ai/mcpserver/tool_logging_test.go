// Checks tool-schema failures are logged without exposing submitted payloads.

package mcpserver

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
)

func TestSubmissionSchemaFailuresAreLoggedWithoutPayload(t *testing.T) {
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
