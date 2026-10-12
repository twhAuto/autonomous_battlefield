package decision

import (
	"fmt"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
)

// Validate construction and recruitment sources against the frozen legal choices.
func validateExpansionChoices(context ai.DecisionContext, plan controller.PlanProposal) []ai.ValidationIssue {
	issues := []ai.ValidationIssue{}
	buildings, sources := map[string]bool{}, map[string]bool{}
	sites := map[controller.Coordinate]bool{}
	for _, b := range context.Legal.Buildings {
		buildings[b.ID] = true
	}
	for _, id := range context.Legal.RecruitmentSources {
		sources[id] = true
	}
	for _, site := range context.Legal.EconomySites {
		sites[site] = true
	}
	if plan.ConstructionOrders != nil {
		for _, order := range *plan.ConstructionOrders {
			if context.Opening || !buildings[order.BuildingType] || !sites[order.Position] {
				issues = append(issues, ai.ValidationIssue{Field: "plan.constructionOrders", Code: "unsupported", Message: fmt.Sprintf("construction %q at (%d,%d) is not legal", order.BuildingType, order.Position.X, order.Position.Y)})
			}
		}
	}
	issues = append(issues, validateRecruitmentSources(plan, sources)...)
	return issues
}

func validateRecruitmentSources(plan controller.PlanProposal, sources map[string]bool) []ai.ValidationIssue {
	issues := []ai.ValidationIssue{}
	if plan.RecruitmentOrders != nil {
		for _, order := range *plan.RecruitmentOrders {
			if order.SourceBuildingID != "" && !sources[order.SourceBuildingID] {
				issues = append(issues, ai.ValidationIssue{Field: "plan.recruitmentOrders", Code: "unsupported", Message: "sourceBuildingId must identify a legal owned recruitment source"})
			}
		}
	}
	return issues
}
