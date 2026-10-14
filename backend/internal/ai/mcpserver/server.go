// Serves decision-context, unit-detail and plan-submission MCP tools behind request-scoped
// capability tokens.

package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai/agentview"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/capability"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/decision"
	"github.com/twh09/autonomous_battlefield/backend/internal/observability/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
	"github.com/twh09/autonomous_battlefield/backend/internal/generation"
)

type requestIDKey struct{}

const (
	bearerPrefix = "Bearer "
)

type Server struct {
	generations    generation.WorkerRepository
	decisions      *decision.Service
	secret         string
	agentSecret    string
	allowedOrigins map[string]bool
	wakeups        *decisionWakeups
	logger         *slog.Logger
}

func New(repository ai.DecisionRepository, secret string, origins []string) *Server {
	return NewWithLogger(repository, secret, origins, slog.Default())
}

func NewWithLogger(repository ai.DecisionRepository, secret string, origins []string, logger *slog.Logger) *Server {
	allowed := make(map[string]bool, len(origins))
	for _, origin := range origins {
		allowed[strings.TrimSpace(origin)] = true
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{decisions: decision.NewService(repository), secret: secret, allowedOrigins: allowed, wakeups: newDecisionWakeups(), logger: logger}
}

func (s *Server) Handler() http.Handler {
	transport := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s.mcpServer() }, &mcp.StreamableHTTPOptions{
		Stateless: true, JSONResponse: true, MaxRequestBodyBytes: 64 << 10, PropagateRequestCancellation: true,
	})
	return s.capabilityHandler(false, transport)
}

// capabilityHandler is the only authentication path for the model-facing MCP endpoints.
// It checks the origin and the capability token, then scopes the request to the
// token's request ID. Battle and generation tokens are signed with the same secret,
// so each endpoint accepts only its own kind: generation IDs carry
// generationLeasePrefix, which is stripped before the tools see the ID.
func (s *Server) capabilityHandler(generationScope bool, transport http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" && !s.allowedOrigins[origin] {
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return
		}
		token, ok := strings.CutPrefix(strings.TrimSpace(r.Header.Get("Authorization")), bearerPrefix)
		if !ok {
			http.Error(w, "missing capability", http.StatusUnauthorized)
			return
		}
		requestID, err := capability.Verify(s.secret, strings.TrimSpace(token), time.Now())
		requestID, isGeneration := strings.CutPrefix(requestID, generationLeasePrefix)
		if err != nil || isGeneration != generationScope {
			http.Error(w, "invalid capability", http.StatusUnauthorized)
			return
		}
		transport.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, requestID)))
	})
}

type getContextInput struct {
	RequestID string `json:"requestId" jsonschema:"request decision identifier"`
}
type getContextOutput struct {
	Context any `json:"context"`
}

type submitPlanInput struct {
	UpgradeOrders          []string                        `json:"upgradeOrders,omitempty" jsonschema:"Ordered one-time upgrade purchases. Include upgrades in spendOrder. Already-owned upgrades are skipped without charging."`
	ConstructionOrders     *[]controller.ConstructionOrder `json:"constructionOrders,omitempty" jsonschema:"Typed construction purchases: buildingType and position from legal choices. Use [] to build nothing; leave constructionPriorities empty."`
	RecruitmentOrders      *[]controller.RecruitmentOrder  `json:"recruitmentOrders,omitempty" jsonschema:"Explicit ordered purchases for this production window. Use [] to buy nothing; at most six units total. Leave recruitmentPriorities empty when using orders."`
	RequestID              string                          `json:"requestId"`
	IdempotencyKey         string                          `json:"idempotencyKey"`
	ExpectedVersion        int64                           `json:"expectedVersion" jsonschema:"Copy context.expectedVersion exactly from get_decision_context; never guess."`
	TargetTick             uint64                          `json:"targetTick" jsonschema:"Copy context.targetTick exactly from get_decision_context; never guess or use the observation tick."`
	Name                   string                          `json:"name"`
	Rationale              string                          `json:"rationale" jsonschema:"A concise 1-3 sentence explanation based on visible battlefield facts. Do not include private chain-of-thought."`
	ActionWeights          map[string]int64                `json:"actionWeights"`
	TargetBonuses          map[string]int64                `json:"targetBonuses,omitempty"`
	ReserveGold            int64                           `json:"reserveGold"`
	SpendOrder             []string                        `json:"spendOrder"`
	ConstructionPriorities []controller.Coordinate         `json:"constructionPriorities"`
	RecruitmentPriorities  []string                        `json:"recruitmentPriorities"`
}

func (s *Server) mcpServer() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "autonomous-battlefield-decisions", Version: ai.ToolSchema}, nil)
	server.AddReceivingMiddleware(s.logToolInputFailures)
	mcp.AddTool(server, &mcp.Tool{Name: "get_unit_details", Description: "Read frozen base stats and guidance for 1–4 distinct legal or visible unit types. Optional; current observations include active modifications."}, s.getUnitDetails)
	mcp.AddTool(server, &mcp.Tool{Name: "get_decision_context", Description: "Read the bounded faction-visible context authorized for this decision request."}, s.getDecisionContext)
	mcp.AddTool(server, &mcp.Tool{Name: "submit_strategy_plan", Description: "Validate and atomically submit the strategy and production plan for this decision request."}, s.submitStrategyPlan)
	return server
}

func authorizedRequestID(ctx context.Context, supplied string) error {
	authorized, _ := ctx.Value(requestIDKey{}).(string)
	if authorized == "" || supplied != authorized {
		return errors.New("request is outside this capability scope")
	}
	return nil
}

func (s *Server) getDecisionContext(ctx context.Context, _ *mcp.CallToolRequest, input getContextInput) (*mcp.CallToolResult, any, error) {
	if err := authorizedRequestID(ctx, input.RequestID); err != nil {
		s.logger.WarnContext(ctx, "decision_context_denied", "request_id", input.RequestID)
		return nil, getContextOutput{}, err
	}
	value, err := s.decisions.GetContext(ctx, input.RequestID)
	if err != nil {
		s.logger.WarnContext(ctx, "decision_context_failed", "request_id", input.RequestID, "error", err)
	} else {
		s.logger.InfoContext(ctx, "decision_context_served", "request_id", input.RequestID, "battle_id", value.BattleID, "faction_id", value.FactionID, "target_tick", value.TargetTick, "observation_version", value.ObservationVersion)
	}
	if err != nil {
		return nil, getContextOutput{}, err
	}
	recordContextSize(ctx, value)
	payload, err := json.Marshal(getContextOutput{Context: agentview.Observation(value)})
	if err != nil {
		return nil, getContextOutput{}, err
	}
	s.logger.InfoContext(ctx, "decision_context_response", "request_id", input.RequestID, "target_tick", value.TargetTick, "expected_version", value.ExpectedVersion, "response_bytes", len(payload))
	// A single JSON text block is readable by both CLI clients and the API
	// runner. Returning nil output prevents the SDK from duplicating this
	// snapshot in structuredContent. Preserve the context envelope and schema.
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(payload)}}}, nil, nil
}

func (s *Server) submitStrategyPlan(ctx context.Context, _ *mcp.CallToolRequest, input submitPlanInput) (*mcp.CallToolResult, ai.SubmissionResult, error) {
	if err := authorizedRequestID(ctx, input.RequestID); err != nil {
		s.logger.WarnContext(ctx, "decision_submission_denied", "request_id", input.RequestID, "target_tick", input.TargetTick)
		return nil, ai.SubmissionResult{}, err
	}
	plan := controller.PlanProposal{ConstructionOrders: input.ConstructionOrders, UpgradeOrders: input.UpgradeOrders, RecruitmentOrders: input.RecruitmentOrders, Name: input.Name, Rationale: input.Rationale, ActionWeights: input.ActionWeights, TargetBonuses: input.TargetBonuses,
		ReserveGold: input.ReserveGold, SpendOrder: input.SpendOrder, ConstructionPriorities: input.ConstructionPriorities,
		RecruitmentPriorities: input.RecruitmentPriorities}
	result, err := s.decisions.SubmitPlan(ctx, ai.PlanSubmission{RequestID: input.RequestID, IdempotencyKey: input.IdempotencyKey,
		ExpectedVersion: input.ExpectedVersion, TargetTick: input.TargetTick, Plan: plan})
	if err != nil {
		s.logger.WarnContext(ctx, "decision_submission_failed", "request_id", input.RequestID, "target_tick", input.TargetTick, "error", err)
	} else {
		for _, issue := range result.Errors {
			s.logger.WarnContext(ctx, "decision_validation_rejected", "request_id", input.RequestID, "target_tick", input.TargetTick, "expected_version", input.ExpectedVersion, "field", issue.Field, "code", issue.Code)
		}
		level := slog.LevelWarn
		if result.Accepted {
			level = slog.LevelInfo
		}
		s.logger.LogAttrs(ctx, level, "decision_plan_submitted",
			slog.String("request_id", input.RequestID), slog.Uint64("target_tick", input.TargetTick), slog.Bool("accepted", result.Accepted),
			slog.String("decision_status", result.Status), slog.Int("validation_error_count", len(result.Errors)))
	}
	trace.SpanFromContext(ctx).SetAttributes(attribute.Bool("battlefield.submission.accepted", result.Accepted))
	tracing.AIOutcome(trace.SpanFromContext(ctx), err != nil || !result.Accepted, "submission_rejected")
	return nil, result, err
}
