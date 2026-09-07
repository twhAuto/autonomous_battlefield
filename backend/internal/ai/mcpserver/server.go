package mcpserver

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
	"github.com/twh09/autonomous_battlefield/backend/internal/generation"
)

type requestIDKey struct{}

const (
	invalidRequestMessage = "invalid request"
	bearerPrefix          = "Bearer "
)

type agentClaimRequest struct {
	JobTypes     []string `json:"jobTypes"`
	WorkerID     string   `json:"workerId"`
	LeaseSeconds int      `json:"leaseSeconds"`
	WaitSeconds  int      `json:"waitSeconds"`
	ProfileIDs   []string `json:"profileIds"`
}

type agentFailureRequest struct {
	RequestID string
	Category  string
	Usage     ai.Usage
	Metadata  map[string]any
}

type agentUsageRequest struct {
	RequestID string
	Usage     ai.Usage
	Metadata  map[string]any
}

type Server struct {
	generations    generation.WorkerRepository
	decisions      *ai.DecisionService
	secret         string
	allowedOrigins map[string]bool
	wakeups        *decisionWakeups
	logger         *slog.Logger
}

type decisionWakeups struct {
	mu      sync.Mutex
	pending chan struct{}
}

func newDecisionWakeups() *decisionWakeups {
	return &decisionWakeups{pending: make(chan struct{})}
}

func (w *decisionWakeups) wait() <-chan struct{} {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.pending
}

func (w *decisionWakeups) notify() {
	w.mu.Lock()
	close(w.pending)
	w.pending = make(chan struct{})
	w.mu.Unlock()
}

// NotifyDecisionAvailable wakes long-polling claim requests. The notification
// is advisory: every awakened worker still atomically claims the durable row.
func (s *Server) NotifyDecisionAvailable() { s.wakeups.notify() }

// AgentHandler is a non-MCP control plane used only to claim provider jobs and
// report runner failures. It does not expose decision context or game state.
func (s *Server) AgentHandler(repository ai.DecisionRepository) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /generation-finish", s.finishGeneration)
	mux.HandleFunc("POST /claim", s.handleAgentClaim(repository))
	mux.HandleFunc("POST /fail", s.handleAgentFailure(repository))
	mux.HandleFunc("POST /usage", s.handleAgentUsage(repository))
	return mux
}

func (s *Server) handleAgentClaim(repository ai.DecisionRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.requireAgentAuthorization(w, r) {
			return
		}
		request, ok := decodeAgentClaimRequest(w, r)
		if !ok {
			return
		}
		var record ai.DecisionRecord
		var found bool
		var err error
		supportsBattle := len(request.JobTypes) == 0 || slices.Contains(request.JobTypes, "battle_decision")
		supportsGeneration := slices.Contains(request.JobTypes, generation.JobType)
		if supportsBattle {
			first := request
			if supportsGeneration {
				first.WaitSeconds = 0
			}
			record, found, err = s.waitForDecisionClaim(r.Context(), repository, first)
		}
		if err != nil {
			s.logger.ErrorContext(r.Context(), "decision_claim_failed", "worker_id", request.WorkerID, "error", err)
			http.Error(w, "claim failed", http.StatusInternalServerError)
			return
		}
		if !found && supportsGeneration {
			// Check drafts before long-polling. Claiming after a full long poll
			// risks leasing a job after the HTTP response deadline has elapsed.
			if s.claimGeneration(w, r, request) {
				return
			}
			if supportsBattle && request.WaitSeconds > 0 {
				record, found, err = s.waitForDecisionClaim(r.Context(), repository, request)
				if err != nil {
					http.Error(w, "claim failed", http.StatusInternalServerError)
					return
				}
			}
		}
		if !found {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		s.logDecisionClaim(r.Context(), request.WorkerID, record)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"requestId": record.ID, "profileId": record.ProfileID, "doctrine": record.Observation.Doctrine, "factionName": record.Observation.FactionName, "background": record.Observation.Background,
			"traceparent": record.Traceparent, "tracestate": record.Tracestate,
		})
	}
}

func decodeAgentClaimRequest(w http.ResponseWriter, r *http.Request) (agentClaimRequest, bool) {
	var request agentClaimRequest
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&request) != nil ||
		request.WorkerID == "" || len(request.ProfileIDs) == 0 || len(request.ProfileIDs) > 32 ||
		request.WaitSeconds < 0 || request.WaitSeconds > 20 || len(request.JobTypes) > 2 {
		http.Error(w, invalidRequestMessage, http.StatusBadRequest)
		return request, false
	}
	for _, jobType := range request.JobTypes {
		if jobType != "battle_decision" && jobType != generation.JobType {
			http.Error(w, invalidRequestMessage, http.StatusBadRequest)
			return request, false
		}
	}
	request.ProfileIDs = normalizedProfileIDs(request.ProfileIDs)
	if len(request.ProfileIDs) == 0 {
		http.Error(w, invalidRequestMessage, http.StatusBadRequest)
		return request, false
	}
	return request, true
}

func (s *Server) waitForDecisionClaim(ctx context.Context, repository ai.DecisionRepository, request agentClaimRequest) (ai.DecisionRecord, bool, error) {
	deadline := time.NewTimer(time.Duration(request.WaitSeconds) * time.Second)
	defer deadline.Stop()
	for {
		// Subscribe before querying so a racing notification cannot be missed.
		wakeup := s.wakeups.wait()
		record, err := repository.ClaimModelDecision(ctx, request.WorkerID, request.ProfileIDs, time.Duration(max(1, request.LeaseSeconds))*time.Second)
		if err == nil {
			return record, true, nil
		}
		if !errors.Is(err, ai.ErrNoDecision) {
			return ai.DecisionRecord{}, false, err
		}
		if request.WaitSeconds == 0 {
			return ai.DecisionRecord{}, false, nil
		}
		select {
		case <-ctx.Done():
			return ai.DecisionRecord{}, false, nil
		case <-deadline.C:
			return ai.DecisionRecord{}, false, nil
		case <-wakeup:
		}
	}
}

func (s *Server) logDecisionClaim(ctx context.Context, workerID string, record ai.DecisionRecord) {
	s.logger.InfoContext(ctx, "decision_claimed", "worker_id", workerID, "request_id", record.ID, "battle_id", record.BattleID,
		"attempt", record.Attempt, "faction_id", record.FactionID, "target_tick", record.TargetTick,
		"profile_id", record.ProfileID, "provider", record.Provider, "runtime", record.Runtime)
}

func (s *Server) handleAgentFailure(repository ai.DecisionRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.requireAgentAuthorization(w, r) {
			return
		}
		var request agentFailureRequest
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&request) != nil || request.RequestID == "" {
			http.Error(w, invalidRequestMessage, http.StatusBadRequest)
			return
		}
		if err := repository.FinishDecisionFailure(r.Context(), request.RequestID, request.Category, request.Usage, request.Metadata); err != nil {
			s.logger.ErrorContext(r.Context(), "decision_failure_update_failed", "request_id", request.RequestID, "error_category", request.Category, "error", err)
			http.Error(w, "failure update failed", http.StatusInternalServerError)
			return
		}
		s.logger.WarnContext(r.Context(), "decision_runner_failed", "request_id", request.RequestID, "error_category", request.Category,
			"runner_outcome", safeMetadataString(request.Metadata, "runnerOutcome"), "input_tokens", request.Usage.InputTokens,
			"output_tokens", request.Usage.OutputTokens, "provider_requests", request.Usage.Requests)
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) handleAgentUsage(repository ai.DecisionRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.requireAgentAuthorization(w, r) {
			return
		}
		var request agentUsageRequest
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&request) != nil || request.RequestID == "" {
			http.Error(w, invalidRequestMessage, http.StatusBadRequest)
			return
		}
		record, err := repository.GetDecision(r.Context(), request.RequestID)
		if err != nil {
			http.Error(w, "decision lookup failed", http.StatusInternalServerError)
			return
		}
		if record.Status != "accepted" {
			http.Error(w, "decision has no accepted plan", http.StatusConflict)
			return
		}
		if err := repository.RecordDecisionUsage(r.Context(), request.RequestID, request.Usage, request.Metadata); err != nil {
			s.logger.ErrorContext(r.Context(), "decision_usage_update_failed", "request_id", request.RequestID, "error", err)
			http.Error(w, "usage update failed", http.StatusInternalServerError)
			return
		}
		s.logger.InfoContext(r.Context(), "decision_usage_recorded", "request_id", request.RequestID,
			"input_tokens", request.Usage.InputTokens, "output_tokens", request.Usage.OutputTokens, "provider_requests", request.Usage.Requests)
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) requireAgentAuthorization(w http.ResponseWriter, r *http.Request) bool {
	if s.agentAuthorized(r) {
		return true
	}
	http.Error(w, "unauthorized", http.StatusUnauthorized)
	return false
}

func (s *Server) agentAuthorized(request *http.Request) bool {
	provided := []byte(strings.TrimSpace(request.Header.Get("Authorization")))
	expected := []byte(bearerPrefix + s.secret)
	return subtle.ConstantTimeCompare(provided, expected) == 1
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
	return &Server{decisions: ai.NewDecisionService(repository), secret: secret, allowedOrigins: allowed, wakeups: newDecisionWakeups(), logger: logger}
}

func safeMetadataString(metadata map[string]any, key string) string {
	value, _ := metadata[key].(string)
	if len(value) > 128 {
		return value[:128]
	}
	return value
}

func normalizedProfileIDs(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > 128 || slices.Contains(result, value) {
			continue
		}
		result = append(result, value)
	}
	return result
}

func (s *Server) Handler() http.Handler {
	transport := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s.mcpServer() }, &mcp.StreamableHTTPOptions{
		Stateless: true, JSONResponse: true, MaxRequestBodyBytes: 64 << 10, PropagateRequestCancellation: true,
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" && !s.allowedOrigins[origin] {
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return
		}
		authorization := strings.TrimSpace(r.Header.Get("Authorization"))
		if !strings.HasPrefix(authorization, bearerPrefix) {
			http.Error(w, "missing capability", http.StatusUnauthorized)
			return
		}
		requestID, err := ai.VerifyCapability(s.secret, strings.TrimSpace(strings.TrimPrefix(authorization, bearerPrefix)), time.Now())
		if err != nil {
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
	Context ai.DecisionContext `json:"context"`
}

type submitPlanInput struct {
	UpgradeOrders          []string                       `json:"upgradeOrders,omitempty" jsonschema:"Ordered one-time upgrade purchases. Include upgrades in spendOrder. Already-owned upgrades are skipped without charging."`
	RecruitmentOrders      *[]controller.RecruitmentOrder `json:"recruitmentOrders,omitempty" jsonschema:"Explicit ordered purchases for this production window. Use [] to buy nothing; at most six units total. Leave recruitmentPriorities empty when using orders."`
	RequestID              string                         `json:"requestId"`
	IdempotencyKey         string                         `json:"idempotencyKey"`
	ExpectedVersion        int64                          `json:"expectedVersion" jsonschema:"Copy context.expectedVersion exactly from get_decision_context; never guess."`
	TargetTick             uint64                         `json:"targetTick" jsonschema:"Copy context.targetTick exactly from get_decision_context; never guess or use the observation tick."`
	Name                   string                         `json:"name"`
	Rationale              string                         `json:"rationale" jsonschema:"A concise 1-3 sentence explanation based on visible battlefield facts. Do not include private chain-of-thought."`
	ActionWeights          map[string]int64               `json:"actionWeights"`
	TargetBonuses          map[string]int64               `json:"targetBonuses,omitempty"`
	ReserveGold            int64                          `json:"reserveGold"`
	SpendOrder             []string                       `json:"spendOrder"`
	ConstructionPriorities []controller.Coordinate        `json:"constructionPriorities"`
	RecruitmentPriorities  []string                       `json:"recruitmentPriorities"`
}

func (s *Server) mcpServer() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "autonomous-battlefield-decisions", Version: ai.ToolSchema}, nil)
	server.AddReceivingMiddleware(s.logToolInputFailures)
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

func (s *Server) getDecisionContext(ctx context.Context, _ *mcp.CallToolRequest, input getContextInput) (*mcp.CallToolResult, getContextOutput, error) {
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
	payload, err := json.Marshal(getContextOutput{Context: value})
	if err != nil {
		return nil, getContextOutput{}, err
	}
	s.logger.InfoContext(ctx, "decision_context_response", "request_id", input.RequestID, "target_tick", value.TargetTick, "expected_version", value.ExpectedVersion, "response_bytes", len(payload))
	// Explicit text supports clients that do not expose structuredContent reliably.
	text := fmt.Sprintf("Decision metadata: requestId=%s targetTick=%d expectedVersion=%d. Copy these values exactly; do not guess. Full faction-visible context JSON follows:\n%s", value.RequestID, value.TargetTick, value.ExpectedVersion, payload)
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, getContextOutput{Context: value}, nil
}

func (s *Server) submitStrategyPlan(ctx context.Context, _ *mcp.CallToolRequest, input submitPlanInput) (*mcp.CallToolResult, ai.SubmissionResult, error) {
	if err := authorizedRequestID(ctx, input.RequestID); err != nil {
		s.logger.WarnContext(ctx, "decision_submission_denied", "request_id", input.RequestID, "target_tick", input.TargetTick)
		return nil, ai.SubmissionResult{}, err
	}
	plan := controller.PlanProposal{UpgradeOrders: input.UpgradeOrders, RecruitmentOrders: input.RecruitmentOrders, Name: input.Name, Rationale: input.Rationale, ActionWeights: input.ActionWeights, TargetBonuses: input.TargetBonuses,
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
	return nil, result, err
}
