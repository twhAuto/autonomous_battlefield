package mcpserver

import (
	"context"
	"encoding/json"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai/agentview"
)

type unitDetailsInput struct {
	GuidanceOffset int      `json:"guidanceOffset,omitempty" jsonschema:"For one unit only: copy nextGuidanceOffset from a previous result to read remaining guidance."`
	RequestID      string   `json:"requestId"`
	UnitIDs        []string `json:"unitIds" jsonschema:"One to four distinct nonempty unit type IDs."`
}

func (s *Server) getUnitDetails(ctx context.Context, _ *mcp.CallToolRequest, input unitDetailsInput) (*mcp.CallToolResult, any, error) {
	if err := authorizedRequestID(ctx, input.RequestID); err != nil {
		return nil, agentview.UnitDetailsResponse{}, err
	}
	s.logger.InfoContext(ctx, "unit_details_requested", "request_id", input.RequestID)
	observation, err := s.decisions.GetContext(ctx, input.RequestID)
	if err != nil {
		return nil, agentview.UnitDetailsResponse{}, err
	}
	value, err := agentview.LookupUnitDetailsPage(observation, input.UnitIDs, input.GuidanceOffset)
	if err != nil {
		return nil, value, err
	}
	// Only IDs from the authorized frozen response are exported, bounded by
	// the lookup's 1–4 limit. Do not export untrusted requested strings.
	ids := make([]string, 0, len(value.Units))
	for _, unit := range value.Units {
		if unit.Available {
			ids = append(ids, unit.UnitID)
		}
	}
	trace.SpanFromContext(ctx).SetAttributes(attribute.StringSlice("battlefield.unit_ids", ids), attribute.Int("battlefield.lookup.requested_count", len(input.UnitIDs)))
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, value, err
	}
	s.logger.InfoContext(ctx, "unit_details_response", "request_id", input.RequestID, "unit_count", len(value.Units), "response_bytes", len(payload))
	// Text-only output avoids duplicating the result in CLI resource rendering.
	// API runners parse this same JSON from the MCP text content.
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(payload)}}}, nil, nil
}
