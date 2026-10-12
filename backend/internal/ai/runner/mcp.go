// Connects runners to MCP with bearer authentication and adapts SDK tool discovery and
// calls to runner contracts.

package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/telemetry"
)

type bearerTransport struct {
	token string
	base  http.RoundTripper
}

func (t bearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	copy := request.Clone(request.Context())
	copy.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(copy)
}

type MCPClient struct{}

type DecisionToolSession interface {
	Tools(context.Context) ([]ai.ToolDefinition, error)
	Call(context.Context, ai.ToolCall) (json.RawMessage, bool, error)
	Close() error
}

type MCPConnector interface {
	Connect(context.Context, string, string) (DecisionToolSession, error)
}

type sdkSession struct{ session *mcp.ClientSession }

func (s sdkSession) Tools(ctx context.Context) ([]ai.ToolDefinition, error) {
	return toolDefinitions(ctx, s.session)
}
func (s sdkSession) Call(ctx context.Context, call ai.ToolCall) (json.RawMessage, bool, error) {
	return callTool(ctx, s.session, call)
}
func (s sdkSession) Close() error { return s.session.Close() }

func (MCPClient) Connect(ctx context.Context, endpoint, token string) (DecisionToolSession, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "battlefield-agent", Version: ai.ToolSchema}, nil)
	httpClient := telemetry.HTTPClient(bearerTransport{token: token, base: http.DefaultTransport})
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: httpClient, MaxRetries: 1, DisableStandaloneSSE: true}, nil)
	if err != nil {
		return nil, err
	}
	return sdkSession{session: session}, nil
}

func toolDefinitions(ctx context.Context, session *mcp.ClientSession) ([]ai.ToolDefinition, error) {
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		return nil, err
	}
	result := make([]ai.ToolDefinition, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		schema, err := json.Marshal(tool.InputSchema)
		if err != nil {
			return nil, err
		}
		result = append(result, ai.ToolDefinition{Name: tool.Name, Description: tool.Description, InputSchema: schema})
	}
	names := make([]string, 0, len(result))
	for _, tool := range result {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	valid := slices.Equal(names, []string{"get_decision_context", "get_unit_details", "submit_strategy_plan"}) || slices.Equal(names, []string{"get_background_context", "submit_faction_background"})
	if !valid {
		return nil, fmt.Errorf("MCP exposed an unsupported tool set")
	}
	return result, nil
}

func callTool(ctx context.Context, session *mcp.ClientSession, call ai.ToolCall) (json.RawMessage, bool, error) {
	var arguments map[string]any
	if err := json.Unmarshal(call.Arguments, &arguments); err != nil {
		return nil, false, err
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: call.Name, Arguments: arguments})
	if err != nil {
		return nil, false, err
	}
	if result.IsError {
		// Preserve schema/handler errors as model-visible tool results so bounded
		// repair (including requesting a smaller batch) can occur in the same session.
		payload, err := json.Marshal(map[string]any{"error": result.Content})
		return payload, false, err
	}
	payload, err := toolResultJSON(result)
	if err != nil {
		return nil, false, err
	}
	accepted := false
	if call.Name == "submit_strategy_plan" || call.Name == "submit_faction_background" {
		var submission ai.SubmissionResult
		if json.Unmarshal(payload, &submission) == nil {
			accepted = submission.Accepted
		}
	}
	return payload, accepted, nil
}

func toolResultJSON(result *mcp.CallToolResult) ([]byte, error) {
	var payload []byte
	var err error
	if result.StructuredContent == nil {
		var text strings.Builder
		for _, content := range result.Content {
			if part, ok := content.(*mcp.TextContent); ok {
				text.WriteString(part.Text)
			}
		}
		payload = []byte(text.String())
		if !json.Valid(payload) {
			return nil, fmt.Errorf("MCP returned no readable JSON result")
		}
	} else {
		payload, err = json.Marshal(result.StructuredContent)
		if err != nil {
			return nil, err
		}
	}
	return payload, nil
}
