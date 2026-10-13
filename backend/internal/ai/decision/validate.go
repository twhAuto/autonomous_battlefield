// Validates plan submissions against the decision's frozen observation and legal
// choices, without reading live game state.

package decision

import (
	"fmt"
	"strings"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
)

// ValidateSubmission checks version, target tick, and plan choices against the
// frozen observation without mutating the decision or simulation. It returns every
// issue at once so a model can correct them in one retry.
func ValidateSubmission(record ai.DecisionRecord, submission ai.PlanSubmission) []ai.ValidationIssue {
	issues := validateBookkeeping(record, submission)
	if err := controller.ValidatePlanProposal(submission.Plan, record.Observation.Legal.ActionKinds); err != nil {
		issues = append(issues, ai.ValidationIssue{Field: "plan", Code: "invalid", Message: err.Error()})
	}
	if isModelRuntime(record.Runtime) && isEmptyPlan(submission.Plan) {
		issues = append(issues, ai.ValidationIssue{Field: "plan", Code: "empty_decision", Message: "Provide an explicit tactical weight, production priority, or gold reserve; a name and rationale alone are not a strategy."})
	}
	legal := record.Observation.Legal
	issues = append(issues, validateUpgrades(legal, submission.Plan)...)
	if record.Observation.Opening {
		issues = append(issues, validateOpening(submission.Plan)...)
	}
	issues = append(issues, validateRecruitment(legal, submission.Plan)...)
	issues = append(issues, validateConstruction(legal, record.Observation.Opening, submission.Plan)...)
	return issues
}

func validateBookkeeping(record ai.DecisionRecord, submission ai.PlanSubmission) []ai.ValidationIssue {
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
	return issues
}

func isModelRuntime(runtime string) bool {
	return runtime == "cli" || runtime == "api"
}

func isEmptyPlan(plan controller.PlanProposal) bool {
	return len(plan.ActionWeights) == 0 && plan.RecruitmentOrders == nil && len(plan.RecruitmentPriorities) == 0 &&
		len(plan.ConstructionPriorities) == 0 && plan.ConstructionOrders == nil && plan.ReserveGold == 0
}

func validateUpgrades(legal ai.LegalChoices, plan controller.PlanProposal) []ai.ValidationIssue {
	var issues []ai.ValidationIssue
	upgrades := map[string]bool{}
	for _, upgrade := range legal.Upgrades {
		upgrades[upgrade.ID] = true
	}
	for _, id := range plan.UpgradeOrders {
		if !upgrades[id] {
			issues = append(issues, ai.ValidationIssue{Field: "plan.upgradeOrders", Code: "unsupported", Message: fmt.Sprintf("upgrade %q is not legal", id)})
		}
	}
	return issues
}

// The opening requires an explicit purchase decision and has no construction.
func validateOpening(plan controller.PlanProposal) []ai.ValidationIssue {
	var issues []ai.ValidationIssue
	if plan.RecruitmentOrders == nil {
		issues = append(issues, ai.ValidationIssue{Field: "plan.recruitmentOrders", Code: "required", Message: "Opening requires explicit orders; use [] to buy nothing."})
	}
	for _, kind := range plan.SpendOrder {
		if kind == "construction" {
			issues = append(issues, ai.ValidationIssue{Field: "plan.spendOrder", Code: "unsupported", Message: "Construction is unavailable during opening."})
		}
	}
	return issues
}

func validateRecruitment(legal ai.LegalChoices, plan controller.PlanProposal) []ai.ValidationIssue {
	var issues []ai.ValidationIssue
	units, sources := map[string]bool{}, map[string]bool{}
	for _, unit := range legal.Units {
		units[unit.ID] = true
	}
	for _, id := range legal.RecruitmentSources {
		sources[id] = true
	}
	for _, unitType := range plan.RecruitmentPriorities {
		if !units[unitType] {
			issues = append(issues, ai.ValidationIssue{Field: "plan.recruitmentPriorities", Code: "unsupported", Message: fmt.Sprintf("unit type %q is not legal", unitType)})
		}
	}
	if plan.RecruitmentOrders == nil {
		return issues
	}
	for _, order := range *plan.RecruitmentOrders {
		if !units[order.UnitType] {
			issues = append(issues, ai.ValidationIssue{Field: "plan.recruitmentOrders", Code: "unsupported", Message: fmt.Sprintf("unit type %q is not legal", order.UnitType)})
		}
		if order.SourceBuildingID != "" && !sources[order.SourceBuildingID] {
			issues = append(issues, ai.ValidationIssue{Field: "plan.recruitmentOrders", Code: "unsupported", Message: "sourceBuildingId must identify a legal owned recruitment source"})
		}
	}
	return issues
}

func validateConstruction(legal ai.LegalChoices, opening bool, plan controller.PlanProposal) []ai.ValidationIssue {
	var issues []ai.ValidationIssue
	buildings, sites := map[string]bool{}, map[controller.Coordinate]bool{}
	for _, building := range legal.Buildings {
		buildings[building.ID] = true
	}
	for _, site := range legal.EconomySites {
		sites[site] = true
	}
	for _, site := range plan.ConstructionPriorities {
		if !sites[site] {
			issues = append(issues, ai.ValidationIssue{Field: "plan.constructionPriorities", Code: "unsupported", Message: fmt.Sprintf("site (%d,%d) is not legal", site.X, site.Y)})
		}
	}
	if plan.ConstructionOrders == nil {
		return issues
	}
	for _, order := range *plan.ConstructionOrders {
		if opening || !buildings[order.BuildingType] || !sites[order.Position] {
			issues = append(issues, ai.ValidationIssue{Field: "plan.constructionOrders", Code: "unsupported", Message: fmt.Sprintf("construction %q at (%d,%d) is not legal", order.BuildingType, order.Position.X, order.Position.Y)})
		}
	}
	return issues
}
