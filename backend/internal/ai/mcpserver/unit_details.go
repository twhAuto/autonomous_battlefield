package mcpserver

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
)

type unitDetailsInput struct {
	GuidanceOffset int      `json:"guidanceOffset,omitempty" jsonschema:"For one unit only: copy nextGuidanceOffset from a previous result to read remaining guidance."`
	RequestID      string   `json:"requestId"`
	UnitIDs        []string `json:"unitIds" jsonschema:"One to four distinct nonempty unit type IDs."`
}

func (s *Server) getUnitDetails(ctx context.Context, _ *mcp.CallToolRequest, input unitDetailsInput) (*mcp.CallToolResult, any, error) {
	if err := authorizedRequestID(ctx, input.RequestID); err != nil {
		return nil, ai.UnitDetailsResponse{}, err
	}
	s.logger.InfoContext(ctx, "unit_details_requested", "request_id", input.RequestID)
	observation, err := s.decisions.GetContext(ctx, input.RequestID)
	if err != nil {
		return nil, ai.UnitDetailsResponse{}, err
	}
	value, err := ai.LookupUnitDetailsPage(observation, input.UnitIDs, input.GuidanceOffset)
	if err != nil {
		return nil, value, err
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, value, err
	}
	s.logger.InfoContext(ctx, "unit_details_response", "request_id", input.RequestID, "unit_count", len(value.Units), "response_bytes", len(payload))
	// Text-only output avoids duplicating the result in CLI resource rendering.
	// API runners parse this same JSON from the MCP text content.
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(payload)}}}, nil, nil
}
