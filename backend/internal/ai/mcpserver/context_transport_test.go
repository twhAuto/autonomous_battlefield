package mcpserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
)

func TestContextTransportSingleJSONCopy(t *testing.T) {
	for _, schema := range []string{ai.ContextSchema, "battlefield.decision-context.v3", "battlefield.decision-context.v2"} {
		t.Run(schema, func(t *testing.T) {
			record := compactRecord()
			record.Observation.SchemaVersion = schema
			server := New(&fakeRepository{record: record}, "secret", nil)
			clientTransport, serverTransport := mcp.NewInMemoryTransports()
			ctx := context.WithValue(context.Background(), requestIDKey{}, "sample")
			ss, err := server.mcpServer().Connect(ctx, serverTransport, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer ss.Close()
			client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
			cs, err := client.Connect(context.Background(), clientTransport, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer cs.Close()
			want, err := json.Marshal(getContextOutput{Context: ai.AgentObservation(record.Observation)})
			if err != nil {
				t.Fatal(err)
			}
			// Repeated reads remain valid and identical; no new call limits.
			for range 2 {
				result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_decision_context", Arguments: map[string]any{"requestId": "sample"}})
				if err != nil {
					t.Fatal(err)
				}
				if result.IsError || result.StructuredContent != nil || len(result.Content) != 1 {
					t.Fatalf("expected one text copy: %+v", result)
				}
				part, ok := result.Content[0].(*mcp.TextContent)
				if !ok || part.Text != string(want) {
					t.Fatal("context envelope or snapshot changed")
				}
				wire, _ := json.Marshal(result)
				oldWire, _ := json.Marshal(&mcp.CallToolResult{Content: result.Content, StructuredContent: json.RawMessage(want)})
				if len(wire) >= len(oldWire) {
					t.Fatal("transport did not shrink")
				}
				t.Logf("context JSON=%d bytes; single-copy transport=%d; duplicate transport=%d", len(want), len(wire), len(oldWire))
			}
		})
	}
}
