// Validates decision submissions against frozen observations and issues or verifies
// request- scoped HMAC capabilities.

package decision

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
)

// DecisionSubmissionRepository is intentionally unable to mutate battle
// state. MCP submissions and the scripted resolver receive only this narrow
// decision-table capability.
type DecisionSubmissionRepository interface {
	GetDecision(context.Context, string) (ai.DecisionRecord, error)
	SubmitDecisionPlan(context.Context, ai.PlanSubmission) (ai.SubmissionResult, error)
}

type DecisionService struct{ repository DecisionSubmissionRepository }

type decisionValidationRecorder interface {
	RecordDecisionValidation(context.Context, string) error
}

func NewDecisionService(repository DecisionSubmissionRepository) *DecisionService {
	return &DecisionService{repository: repository}
}

func (s *DecisionService) GetContext(ctx context.Context, requestID string) (ai.DecisionContext, error) {
	record, err := s.repository.GetDecision(ctx, requestID)
	if err != nil {
		return ai.DecisionContext{}, err
	}
	return record.Observation, nil
}

// SubmitPlan checks the persisted decision and its legal choices before writing
// a plan; validation rejection is returned in the result rather than as an error.
func (s *DecisionService) SubmitPlan(ctx context.Context, submission ai.PlanSubmission) (ai.SubmissionResult, error) {
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

func (s *DecisionService) rejectInvalidPlan(ctx context.Context, record ai.DecisionRecord, requestID string, issues []ai.ValidationIssue) (ai.SubmissionResult, error) {
	if recorder, ok := s.repository.(decisionValidationRecorder); ok {
		if err := recorder.RecordDecisionValidation(ctx, requestID); err != nil {
			return ai.SubmissionResult{}, err
		}
	}
	rejected := ai.SubmissionResult{Status: "rejected", Errors: issues}
	if record.Runtime != "cli" && record.Runtime != "api" {
		return rejected, nil
	}
	countJSON, _ := json.Marshal(record.ProviderMetadata["validationFailures"])
	var count int
	_ = json.Unmarshal(countJSON, &count)
	if count < 2 {
		return rejected, nil
	}
	repo, ok := s.repository.(interface {
		FinishDecisionFailure(context.Context, string, string, ai.Usage, map[string]any) error
	})
	if !ok {
		return rejected, nil
	}
	if err := repo.FinishDecisionFailure(ctx, requestID, "validation_limit", ai.Usage{}, map[string]any{"runnerOutcome": "validation_limit"}); err != nil {
		return ai.SubmissionResult{}, err
	}
	return ai.SubmissionResult{Status: "failed", Errors: []ai.ValidationIssue{{Field: "plan", Code: "validation_limit", Message: "Three invalid submissions. Decision closed; stop submitting."}}}, nil
}

// ValidateSubmission checks version, target tick, and plan choices against the
// frozen observation without mutating the decision or simulation.
func ValidateSubmission(record ai.DecisionRecord, submission ai.PlanSubmission) []ai.ValidationIssue {
	issues := make([]ai.ValidationIssue, 0)
	if strings.TrimSpace(submission.IdempotencyKey) == "" {
		issues = append(issues, ai.ValidationIssue{Field: "idempotencyKey", Code: "required", Message: "idempotencyKey is required"})
	}
	if submission.ExpectedVersion != record.Version {
		issues = append(issues, ai.ValidationIssue{Field: "expectedVersion", Code: "conflict", Message: fmt.Sprintf("Use expectedVersion=%d from get_decision_context", record.Version)})
	}
	if submission.TargetTick != record.TargetTick {
		issues = append(issues, ai.ValidationIssue{Field: "targetTick", Code: "stale", Message: fmt.Sprintf("Use targetTick=%d from get_decision_context; do not guess", record.TargetTick)})
	}
	if err := controller.ValidatePlanProposal(submission.Plan, record.Observation.Legal.ActionKinds); err != nil {
		issues = append(issues, ai.ValidationIssue{Field: "plan", Code: "invalid", Message: err.Error()})
	}
	if (record.Runtime == "cli" || record.Runtime == "api") && len(submission.Plan.ActionWeights) == 0 && submission.Plan.RecruitmentOrders == nil && len(submission.Plan.RecruitmentPriorities) == 0 && len(submission.Plan.ConstructionPriorities) == 0 && submission.Plan.ConstructionOrders == nil && submission.Plan.ReserveGold == 0 {
		issues = append(issues, ai.ValidationIssue{Field: "plan", Code: "empty_decision", Message: "Provide an explicit tactical weight, production priority, or gold reserve; a name and rationale alone are not a strategy."})
	}
	issues = append(issues, validateRecruitmentChoices(record, submission.Plan)...)
	legalSites := make(map[controller.Coordinate]bool, len(record.Observation.Legal.EconomySites))
	for _, site := range record.Observation.Legal.EconomySites {
		legalSites[site] = true
	}
	for _, site := range submission.Plan.ConstructionPriorities {
		if !legalSites[site] {
			issues = append(issues, ai.ValidationIssue{Field: "plan.constructionPriorities", Code: "unsupported", Message: fmt.Sprintf("site (%d,%d) is not legal", site.X, site.Y)})
		}
	}
	issues = append(issues, validateExpansionChoices(record.Observation, submission.Plan)...)
	return issues
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

func validateRecruitmentChoices(record ai.DecisionRecord, plan controller.PlanProposal) []ai.ValidationIssue {
	var issues []ai.ValidationIssue
	issues = append(issues, validateUpgradeAndOpeningChoices(record, plan)...)
	legalUnits := make(map[string]bool, len(record.Observation.Legal.Units))
	for _, unit := range record.Observation.Legal.Units {
		legalUnits[unit.ID] = true
	}
	for _, unitType := range plan.RecruitmentPriorities {
		if !legalUnits[unitType] {
			issues = append(issues, ai.ValidationIssue{Field: "plan.recruitmentPriorities", Code: "unsupported", Message: fmt.Sprintf("unit type %q is not legal", unitType)})
		}
	}
	if plan.RecruitmentOrders != nil {
		for _, order := range *plan.RecruitmentOrders {
			if !legalUnits[order.UnitType] {
				issues = append(issues, ai.ValidationIssue{Field: "plan.recruitmentOrders", Code: "unsupported", Message: fmt.Sprintf("unit type %q is not legal", order.UnitType)})
			}
		}
	}

	return issues
}

func validateUpgradeAndOpeningChoices(record ai.DecisionRecord, plan controller.PlanProposal) []ai.ValidationIssue {
	var issues []ai.ValidationIssue
	legalUpgrades := map[string]bool{}
	for _, upgrade := range record.Observation.Legal.Upgrades {
		legalUpgrades[upgrade.ID] = true
	}
	for _, id := range plan.UpgradeOrders {
		if !legalUpgrades[id] {
			issues = append(issues, ai.ValidationIssue{Field: "plan.upgradeOrders", Code: "unsupported", Message: fmt.Sprintf("upgrade %q is not legal", id)})
		}
	}
	if record.Observation.Opening {
		if plan.RecruitmentOrders == nil {
			issues = append(issues, ai.ValidationIssue{Field: "plan.recruitmentOrders", Code: "required", Message: "Opening requires explicit orders; use [] to buy nothing."})
		}
		for _, kind := range plan.SpendOrder {
			if kind == "construction" {
				issues = append(issues, ai.ValidationIssue{Field: "plan.spendOrder", Code: "unsupported", Message: "Construction is unavailable during opening."})
			}
		}
	}
	return issues
}
