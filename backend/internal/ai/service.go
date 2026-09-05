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
	issues := ValidateSubmission(record, submission)
	if len(issues) > 0 {
		if recorder, ok := s.repository.(decisionValidationRecorder); ok {
			if err := recorder.RecordDecisionValidation(ctx, submission.RequestID); err != nil {
				return SubmissionResult{}, err
			}
		}
		return SubmissionResult{Status: "rejected", Errors: issues}, nil
	}
	return s.repository.SubmitDecisionPlan(ctx, submission)
}

func ValidateSubmission(record DecisionRecord, submission PlanSubmission) []ValidationIssue {
	issues := make([]ValidationIssue, 0)
	if strings.TrimSpace(submission.IdempotencyKey) == "" {
		issues = append(issues, ValidationIssue{Field: "idempotencyKey", Code: "required", Message: "idempotencyKey is required"})
	}
	if submission.ExpectedVersion != record.Version {
		issues = append(issues, ValidationIssue{Field: "expectedVersion", Code: "conflict", Message: "decision version changed"})
	}
	if submission.TargetTick != record.TargetTick {
		issues = append(issues, ValidationIssue{Field: "targetTick", Code: "stale", Message: "target tick does not match"})
	}
	if err := controller.ValidatePlanProposal(submission.Plan, record.Observation.Legal.ActionKinds); err != nil {
		issues = append(issues, ValidationIssue{Field: "plan", Code: "invalid", Message: err.Error()})
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
