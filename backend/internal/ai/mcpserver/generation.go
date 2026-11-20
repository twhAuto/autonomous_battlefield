// Exposes capability-scoped background-draft MCP tools.

package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai/prompts"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/faction"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/generation"
)

const generationLeasePrefix = "generation:"

func (s *Server) WithGenerations(repo generation.WorkerRepository) *Server {
	s.generations = repo
	return s
}

type backgroundInput struct {
	RequestID  string `json:"requestId"`
	Background string `json:"background"`
}
type backgroundContext struct {
	RequestID string `json:"requestId"`
	generation.Input
}
type backgroundOutput struct {
	Context backgroundContext `json:"context"`
}

func (s *Server) backgroundServer() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "faction-background", Version: prompts.GenerationVersion}, nil)
	server.AddReceivingMiddleware(s.logToolInputFailures)
	mcp.AddTool(server, &mcp.Tool{Name: "get_background_context", Description: "Read the draft faction profile for this generation request."}, func(ctx context.Context, _ *mcp.CallToolRequest, input getContextInput) (*mcp.CallToolResult, backgroundOutput, error) {
		if err := authorizedRequestID(ctx, input.RequestID); err != nil {
			return nil, backgroundOutput{}, err
		}
		job, err := s.generations.Context(ctx, input.RequestID)
		if err != nil {
			return nil, backgroundOutput{}, err
		}
		return nil, backgroundOutput{Context: backgroundContext{RequestID: job.ID, Input: job.Input}}, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "submit_faction_background", Description: fmt.Sprintf("Submit 1-%d Unicode characters of background. This returns a draft; it never saves a faction.", faction.MaxBackgroundLength)}, func(ctx context.Context, _ *mcp.CallToolRequest, input backgroundInput) (*mcp.CallToolResult, ai.SubmissionResult, error) {
		if err := authorizedRequestID(ctx, input.RequestID); err != nil {
			return nil, ai.SubmissionResult{}, err
		}
		accepted, err := s.generations.Submit(ctx, input.RequestID, input.Background)
		if errors.Is(err, generation.ErrInvalid) {
			return nil, ai.SubmissionResult{Status: "rejected", Errors: []ai.ValidationIssue{{Field: "background", Code: "invalid", Message: fmt.Sprintf("Submit between 1 and %d Unicode characters.", faction.MaxBackgroundLength)}}}, nil
		}
		return nil, ai.SubmissionResult{Accepted: accepted, Status: map[bool]string{true: "accepted", false: "closed"}[accepted]}, err
	})
	return server
}
func (s *Server) GenerationHandler() http.Handler {
	transport := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s.backgroundServer() }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: 64 << 10, PropagateRequestCancellation: true})
	authenticated := s.capabilityHandler(true, transport)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.generations == nil {
			http.Error(w, "generation unavailable", http.StatusServiceUnavailable)
			return
		}
		authenticated.ServeHTTP(w, r)
	})
}
