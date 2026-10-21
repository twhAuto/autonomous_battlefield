// Serves frozen decision contexts and accepts plan submissions after validation, closing
// a model decision after repeated invalid plans.

package decision

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
)

// validationLimit is the number of invalid model submissions that closes a decision.
const validationLimit = 3

// SubmissionRepository is intentionally unable to mutate battle state. The AI
// gateway and the engine's scripted controllers receive only this narrow
// decision-table capability.
type SubmissionRepository interface {
	GetDecision(context.Context, string) (ai.DecisionRecord, error)
	SubmitDecisionPlan(context.Context, ai.PlanSubmission) (ai.SubmissionResult, error)
	RecordDecisionValidation(context.Context, string) (int, error)
	FinishDecisionFailure(context.Context, string, string, ai.Usage, map[string]any) error
}

type Service struct{ repository SubmissionRepository }

func NewService(repository SubmissionRepository) *Service {
	return &Service{repository: repository}
}

func (s *Service) GetContext(ctx context.Context, requestID string) (ai.DecisionContext, error) {
	record, err := s.repository.GetDecision(ctx, requestID)
	if err != nil {
		return ai.DecisionContext{}, err
	}
	if recorder, ok := s.repository.(interface {
		RecordDecisionContextServed(context.Context, string) error
	}); ok {
		if err := recorder.RecordDecisionContextServed(ctx, requestID); err != nil {
			slog.WarnContext(ctx, "decision_context_timing_failed", "request_id", requestID, "error", err)
		}
	}
	return record.Observation, nil
}

// SubmitPlan checks the persisted decision and its legal choices before writing
// a plan; validation rejection is returned in the result rather than as an error.
func (s *Service) SubmitPlan(ctx context.Context, submission ai.PlanSubmission) (ai.SubmissionResult, error) {
	record, err := s.repository.GetDecision(ctx, submission.RequestID)
	if err != nil {
		return ai.SubmissionResult{}, err
	}
	if record.Status == "failed" || record.Status == "fallback" {
		return ai.SubmissionResult{Status: "failed", Errors: []ai.ValidationIssue{{Field: "requestId", Code: "terminal", Message: "Decision is closed. Stop submitting."}}}, nil
	}
	issues := ValidateSubmission(record, submission)
	if len(issues) > 0 {
		return s.rejectInvalidPlan(ctx, record, submission.RequestID, issues)
	}
	return s.repository.SubmitDecisionPlan(ctx, submission)
}

// The repository returns the count after its atomic increment, so concurrent invalid
// submissions cannot both observe a count below the limit.
func (s *Service) rejectInvalidPlan(ctx context.Context, record ai.DecisionRecord, requestID string, issues []ai.ValidationIssue) (ai.SubmissionResult, error) {
	failures, err := s.repository.RecordDecisionValidation(ctx, requestID)
	if err != nil {
		return ai.SubmissionResult{}, err
	}
	if !isModelRuntime(record.Runtime) || failures < validationLimit {
		return ai.SubmissionResult{Status: "rejected", Errors: issues}, nil
	}
	if err := s.repository.FinishDecisionFailure(ctx, requestID, "validation_limit", ai.Usage{}, map[string]any{"runnerOutcome": "validation_limit"}); err != nil {
		return ai.SubmissionResult{}, err
	}
	return ai.SubmissionResult{Status: "failed", Errors: []ai.ValidationIssue{{Field: "plan", Code: "validation_limit", Message: "Three invalid submissions. Decision closed; stop submitting."}}}, nil
}

// ObservationHash fingerprints the decision context while excluding its request
// ID and hash field, so those bookkeeping fields do not change the fingerprint.
func ObservationHash(context ai.DecisionContext) (string, error) {
	copy := context
	copy.RequestID = ""
	copy.ObservationHash = ""
	payload, err := json.Marshal(copy)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(payload)
	return hex.EncodeToString(hash[:]), nil
}
