package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
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
	httpClient := &http.Client{Transport: bearerTransport{token: token, base: http.DefaultTransport}}
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
	if len(result) != 2 {
		return nil, fmt.Errorf("MCP exposed %d tools, want exactly 2", len(result))
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
	payload, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return nil, false, err
	}
	accepted := false
	if call.Name == "submit_strategy_plan" {
		var submission ai.SubmissionResult
		if json.Unmarshal(payload, &submission) == nil {
			accepted = submission.Accepted
		}
	}
	return payload, accepted, nil
}
