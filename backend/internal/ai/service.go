package ai

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
)

// DecisionSubmissionRepository is intentionally unable to mutate battle
// state. MCP submissions and the scripted resolver receive only this narrow
// decision-table capability.
type DecisionSubmissionRepository interface {
	GetDecision(context.Context, string) (DecisionRecord, error)
	SubmitDecisionPlan(context.Context, PlanSubmission) (SubmissionResult, error)
}

type DecisionService struct{ repository DecisionSubmissionRepository }

type decisionValidationRecorder interface {
	RecordDecisionValidation(context.Context, string) error
}

func NewDecisionService(repository DecisionSubmissionRepository) *DecisionService {
	return &DecisionService{repository: repository}
}

func (s *DecisionService) GetContext(ctx context.Context, requestID string) (DecisionContext, error) {
	record, err := s.repository.GetDecision(ctx, requestID)
	if err != nil {
		return DecisionContext{}, err
	}
	return record.Observation, nil
}

func (s *DecisionService) SubmitPlan(ctx context.Context, submission PlanSubmission) (SubmissionResult, error) {
	record, err := s.repository.GetDecision(ctx, submission.RequestID)
	if err != nil {
		return SubmissionResult{}, err
	}
	if record.Status == "failed" || record.Status == "fallback" {
		return SubmissionResult{Status: "failed", Errors: []ValidationIssue{{Field: "requestId", Code: "terminal", Message: "Decision is closed. Stop submitting."}}}, nil
	}
	issues := ValidateSubmission(record, submission)
	if len(issues) > 0 {
		return s.rejectInvalidPlan(ctx, record, submission.RequestID, issues)
	}
	return s.repository.SubmitDecisionPlan(ctx, submission)
}

func (s *DecisionService) rejectInvalidPlan(ctx context.Context, record DecisionRecord, requestID string, issues []ValidationIssue) (SubmissionResult, error) {
	if recorder, ok := s.repository.(decisionValidationRecorder); ok {
		if err := recorder.RecordDecisionValidation(ctx, requestID); err != nil {
			return SubmissionResult{}, err
		}
	}
	rejected := SubmissionResult{Status: "rejected", Errors: issues}
	if record.Runtime != "cli" {
		return rejected, nil
	}
	countJSON, _ := json.Marshal(record.ProviderMetadata["validationFailures"])
	var count int
	_ = json.Unmarshal(countJSON, &count)
	if count < 2 {
		return rejected, nil
	}
	repo, ok := s.repository.(interface {
		FinishDecisionFailure(context.Context, string, string, Usage, map[string]any) error
	})
	if !ok {
		return rejected, nil
	}
	if err := repo.FinishDecisionFailure(ctx, requestID, "validation_limit", Usage{}, map[string]any{"runnerOutcome": "validation_limit"}); err != nil {
		return SubmissionResult{}, err
	}
	return SubmissionResult{Status: "failed", Errors: []ValidationIssue{{Field: "plan", Code: "validation_limit", Message: "Three invalid submissions. Decision closed; stop submitting."}}}, nil
}

func ValidateSubmission(record DecisionRecord, submission PlanSubmission) []ValidationIssue {
	issues := make([]ValidationIssue, 0)
	if strings.TrimSpace(submission.IdempotencyKey) == "" {
		issues = append(issues, ValidationIssue{Field: "idempotencyKey", Code: "required", Message: "idempotencyKey is required"})
	}
	if submission.ExpectedVersion != record.Version {
		issues = append(issues, ValidationIssue{Field: "expectedVersion", Code: "conflict", Message: fmt.Sprintf("Use expectedVersion=%d from get_decision_context", record.Version)})
	}
	if submission.TargetTick != record.TargetTick {
		issues = append(issues, ValidationIssue{Field: "targetTick", Code: "stale", Message: fmt.Sprintf("Use targetTick=%d from get_decision_context; do not guess", record.TargetTick)})
	}
	if err := controller.ValidatePlanProposal(submission.Plan, record.Observation.Legal.ActionKinds); err != nil {
		issues = append(issues, ValidationIssue{Field: "plan", Code: "invalid", Message: err.Error()})
	}
	if record.Runtime == "cli" && len(submission.Plan.ActionWeights) == 0 && len(submission.Plan.RecruitmentPriorities) == 0 && len(submission.Plan.ConstructionPriorities) == 0 && submission.Plan.ReserveGold == 0 {
		issues = append(issues, ValidationIssue{Field: "plan", Code: "empty_decision", Message: "Provide an explicit tactical weight, production priority, or gold reserve; a name and rationale alone are not a strategy."})
	}
	legalUnits := make(map[string]bool, len(record.Observation.Legal.Units))
	for _, unit := range record.Observation.Legal.Units {
		legalUnits[unit.ID] = true
	}
	for _, unitType := range submission.Plan.RecruitmentPriorities {
		if !legalUnits[unitType] {
			issues = append(issues, ValidationIssue{Field: "plan.recruitmentPriorities", Code: "unsupported", Message: fmt.Sprintf("unit type %q is not legal", unitType)})
		}
	}
	legalSites := make(map[controller.Coordinate]bool, len(record.Observation.Legal.EconomySites))
	for _, site := range record.Observation.Legal.EconomySites {
		legalSites[site] = true
	}
	for _, site := range submission.Plan.ConstructionPriorities {
		if !legalSites[site] {
			issues = append(issues, ValidationIssue{Field: "plan.constructionPriorities", Code: "unsupported", Message: fmt.Sprintf("site (%d,%d) is not legal", site.X, site.Y)})
		}
	}
	return issues
}

func ObservationHash(context DecisionContext) (string, error) {
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

type capabilityClaims struct {
	RequestID string `json:"requestId"`
	ExpiresAt int64  `json:"expiresAt"`
}

func MintCapability(secret, requestID string, expiresAt time.Time) (string, error) {
	if secret == "" || requestID == "" {
		return "", errors.New("capability secret and request ID are required")
	}
	payload, err := json.Marshal(capabilityClaims{RequestID: requestID, ExpiresAt: expiresAt.Unix()})
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func VerifyCapability(secret, token string, now time.Time) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return "", errors.New("invalid capability token")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", errors.New("invalid capability token")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(parts[0]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return "", errors.New("invalid capability token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", errors.New("invalid capability token")
	}
	var claims capabilityClaims
	if json.Unmarshal(payload, &claims) != nil || claims.RequestID == "" || now.Unix() >= claims.ExpiresAt {
		return "", errors.New("expired capability token")
	}
	return claims.RequestID, nil
}
