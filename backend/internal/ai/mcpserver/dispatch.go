// Agent dispatch API: authenticates workers with the agent secret, long-polls for
// decision and background-generation jobs, mints per-job capability tokens, and records
// runner failures and usage. It is plain JSON over HTTP, not MCP, and never exposes
// decision context or game state.

package mcpserver

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/capability"
	"github.com/twh09/autonomous_battlefield/backend/internal/generation"
)

const invalidRequestMessage = "invalid request"

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
	mux.HandleFunc("GET /enabled", func(w http.ResponseWriter, r *http.Request) {
		if !s.requireAgentAuthorization(w, r) {
			return
		}
		policy, ok := repository.(interface {
			AgentEnabled(context.Context) (bool, error)
		})
		if !ok {
			http.Error(w, "policy unavailable", 503)
			return
		}
		enabled, err := policy.AgentEnabled(r.Context())
		if err != nil || !enabled {
			http.Error(w, "AI disabled", 503)
			return
		}
		w.WriteHeader(204)
	})
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
		supportsBattle := len(request.JobTypes) == 0 || slices.Contains(request.JobTypes, "battle_decision")
		supportsGeneration := slices.Contains(request.JobTypes, generation.JobType)
		record, found, err := s.initialDecisionClaim(r.Context(), repository, request, supportsBattle, supportsGeneration)
		if err != nil {
			s.logger.ErrorContext(r.Context(), "decision_claim_failed", "worker_id", request.WorkerID, "error", err)
			http.Error(w, "claim failed", http.StatusInternalServerError)
			return
		}
		if !found && supportsGeneration {
			var handled bool
			record, found, handled = s.claimAfterGeneration(w, r, repository, request, supportsBattle)
			if handled {
				return
			}
		}
		if !found {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		s.writeDecisionClaim(w, r, request.WorkerID, record)
	}
}

func (s *Server) writeDecisionClaim(w http.ResponseWriter, r *http.Request, workerID string, record ai.DecisionRecord) {
	if record.LeaseExpiresAt == nil {
		http.Error(w, "missing execution deadline", 503)
		return
	}
	token, err := capability.MintCapability(s.secret, record.ID, *record.LeaseExpiresAt)
	if err != nil {
		http.Error(w, "capability unavailable", 503)
		return
	}
	s.logDecisionClaim(r.Context(), workerID, record)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"capabilityToken": token, "executionDeadline": record.LeaseExpiresAt,
		"requestId": record.ID, "profileId": record.ProfileID, "doctrine": record.Observation.Doctrine, "factionName": record.Observation.FactionName, "background": record.Observation.Background,
		"traceparent": record.Traceparent, "tracestate": record.Tracestate,
		"battleId": record.BattleID, "factionId": record.FactionID, "attempt": record.Attempt, "targetTick": record.TargetTick,
	})
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
	request.LeaseSeconds = max(1, min(300, request.LeaseSeconds))
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
	if s.agentSecret == "" || s.agentSecret == s.secret {
		return false
	}
	provided := []byte(strings.TrimSpace(request.Header.Get("Authorization")))
	expected := []byte(bearerPrefix + s.agentSecret)
	return subtle.ConstantTimeCompare(provided, expected) == 1
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

func (s *Server) initialDecisionClaim(ctx context.Context, repository ai.DecisionRepository, request agentClaimRequest, supportsBattle, supportsGeneration bool) (ai.DecisionRecord, bool, error) {
	var record ai.DecisionRecord
	var found bool
	var err error
	if supportsBattle {
		first := request
		if supportsGeneration {
			first.WaitSeconds = 0
		}
		record, found, err = s.waitForDecisionClaim(ctx, repository, first)
	}

	return record, found, err
}

func (s *Server) claimAfterGeneration(w http.ResponseWriter, r *http.Request, repository ai.DecisionRepository, request agentClaimRequest, supportsBattle bool) (ai.DecisionRecord, bool, bool) {
	var record ai.DecisionRecord
	var found bool
	var err error
	// Check drafts before long-polling. Claiming after a full long poll
	// risks leasing a job after the HTTP response deadline has elapsed.
	if s.claimGeneration(w, r, request) {
		return record, found, true
	}
	if supportsBattle && request.WaitSeconds > 0 {
		record, found, err = s.waitForDecisionClaim(r.Context(), repository, request)
		if err != nil {
			http.Error(w, "claim failed", http.StatusInternalServerError)
			return record, found, true
		}
	}

	return record, found, false
}

func (s *Server) WithAgentSecret(secret string) *Server { s.agentSecret = secret; return s }
func (s *Server) mint(id string, ttl time.Duration) string {
	token, _ := capability.MintCapability(s.secret, id, time.Now().Add(ttl))
	return token
}

func (s *Server) claimGeneration(w http.ResponseWriter, r *http.Request, request agentClaimRequest) bool {
	if s.generations == nil {
		return false
	}
	job, err := s.generations.Claim(r.Context(), request.WorkerID, request.ProfileIDs)
	if errors.Is(err, generation.ErrNotFound) {
		return false
	}
	if err != nil {
		http.Error(w, "generation claim failed", 500)
		return true
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"executionDeadline": job.ExpiresAt, "capabilityToken": s.mint(generationLeasePrefix+job.ID, time.Until(job.ExpiresAt)), "jobType": generation.JobType, "requestId": job.ID, "profileId": job.ProfileID, "executionTimeoutSeconds": job.ExecutionSeconds})
	return true
}
func (s *Server) finishGeneration(w http.ResponseWriter, r *http.Request) {
	if !s.requireAgentAuthorization(w, r) {
		return
	}
	if s.generations == nil {
		http.Error(w, "generation unavailable", 503)
		return
	}
	var body struct {
		RequestID string         `json:"requestId"`
		Category  string         `json:"category"`
		Usage     ai.Usage       `json:"usage"`
		Metadata  map[string]any `json:"metadata"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body) != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	ok, err := s.generations.Finish(r.Context(), body.RequestID, body.Category, body.Usage, body.Metadata)
	if err != nil {
		http.Error(w, "generation report failed", 500)
		return
	}
	if !ok {
		http.Error(w, "generation closed", 409)
		return
	}
	w.WriteHeader(204)
}
