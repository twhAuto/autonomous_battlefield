// Validates plan submissions against the decision's frozen observation and legal
// choices, without reading live game state.

package decision

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
)

const (
	fieldRecruitmentOrders  = "plan.recruitmentOrders"
	fieldConstructionOrders = "plan.constructionOrders"
	fieldProductionOrders   = "plan.productionOrders"
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
	if err := game.ValidateUpgradeOrder(legal.Upgrades, record.Observation.OwnedUpgradeIDs, submission.Plan.UpgradeOrders); err != nil {
		issues = append(issues, ai.ValidationIssue{Field: "plan.upgradeOrders", Code: "invalid_order", Message: err.Error()})
	}
	if record.Observation.Opening {
		issues = append(issues, validateOpeningForMode(submission.Plan, record.Observation.ContinuousProduction)...)
	}
	if err := game.ValidateBuildingProduction(record.Observation.ContinuousProduction, record.FactionID, record.Observation.View.State, legal.RecruitmentSources, submission.Plan); err != nil {
		issues = append(issues, ai.ValidationIssue{Field: fieldProductionOrders, Code: "unsupported", Message: err.Error()})
	}
	if err := game.ValidateUnitUnlocks(legal.Units, record.Observation.OwnedUpgradeIDs, submission.Plan); err != nil {
		issues = append(issues, ai.ValidationIssue{Field: fieldProductionOrders, Code: "locked", Message: err.Error()})
	}
	issues = append(issues, validateRecruitment(legal, submission.Plan)...)
	issues = append(issues, validateTrainingSources(record.Observation, submission.Plan)...)
	goldMines := len(record.Observation.View.State.GoldMines) > 0
	issues = append(issues, validateConstruction(legal, record.Observation.Opening, goldMines, submission.Plan)...)
	// Scripted plans may over-order; the engine skips orders beyond a limit.
	if isModelRuntime(record.Runtime) {
		issues = append(issues, validateBuildingLimits(record.Observation.Economy, submission.Plan)...)
	}
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
	return plan.ProductionOrders == nil && len(plan.ActionWeights) == 0 && plan.RecruitmentOrders == nil && len(plan.RecruitmentPriorities) == 0 &&
		len(plan.ConstructionPriorities) == 0 && plan.ConstructionOrders == nil && len(plan.UpgradeOrders) == 0 && plan.ReserveGold == 0
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

func validateOpeningForMode(plan controller.PlanProposal, continuous bool) []ai.ValidationIssue {
	var issues []ai.ValidationIssue
	if continuous && plan.ProductionOrders == nil {
		issues = append(issues, ai.ValidationIssue{Field: fieldProductionOrders, Code: "required", Message: "Opening requires explicit production orders; use [] to leave production off."})
	}
	if !continuous && plan.RecruitmentOrders == nil {
		issues = append(issues, ai.ValidationIssue{Field: fieldRecruitmentOrders, Code: "required", Message: "Opening requires explicit orders; use [] to buy nothing."})
	}
	for _, kind := range plan.SpendOrder {
		if kind == "construction" {
			issues = append(issues, ai.ValidationIssue{Field: "plan.spendOrder", Code: "unsupported", Message: "Remove construction from spendOrder: the opening has no construction."})
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
			issues = append(issues, ai.ValidationIssue{Field: fieldRecruitmentOrders, Code: "unsupported", Message: fmt.Sprintf("unit type %q is not legal", order.UnitType)})
		}
		if order.SourceBuildingID != "" && !sources[order.SourceBuildingID] {
			issues = append(issues, ai.ValidationIssue{Field: fieldRecruitmentOrders, Code: "unsupported", Message: "sourceBuildingId must identify a legal owned recruitment source"})
		}
	}
	return issues
}

// With gold mines, economy (including legacy priorities) may use only goldMineSites
// and every other structure only economySites.
func validateConstruction(legal ai.LegalChoices, opening, goldMines bool, plan controller.PlanProposal) []ai.ValidationIssue {
	var issues []ai.ValidationIssue
	buildings := map[string]bool{}
	for _, building := range legal.Buildings {
		buildings[building.ID] = true
	}
	sites := siteList{name: "legal.economySites", sites: legal.EconomySites}
	economySites := sites
	if goldMines {
		economySites = siteList{name: "legal.goldMineSites", sites: legal.GoldMineSites}
	}
	for _, site := range plan.ConstructionPriorities {
		if !economySites.has(site) {
			issues = append(issues, ai.ValidationIssue{Field: "plan.constructionPriorities", Code: "unsupported", Message: fmt.Sprintf("site (%d,%d) is not in %s%s", site.X, site.Y, economySites.name, economySites.nearest(site))})
		}
	}
	if plan.ConstructionOrders == nil {
		return issues
	}
	for _, order := range *plan.ConstructionOrders {
		if message := constructionOrderProblem(order, opening, buildings, sites, economySites); message != "" {
			issues = append(issues, ai.ValidationIssue{Field: fieldConstructionOrders, Code: "unsupported", Message: message})
		}
	}
	return issues
}

// constructionOrderProblem names what makes an order illegal and how to fix it, or
// returns "" for a legal order.
func constructionOrderProblem(order controller.ConstructionOrder, opening bool, buildings map[string]bool, sites, economySites siteList) string {
	if opening {
		return "construction is unavailable during the opening; send constructionOrders as []"
	}
	if !buildings[order.BuildingType] {
		return fmt.Sprintf("construction %q is not in legal.buildings", order.BuildingType)
	}
	legal := sites
	if order.BuildingType == game.BuildingEconomy {
		legal = economySites
	}
	if legal.has(order.Position) {
		return ""
	}
	return fmt.Sprintf("construction %q at (%d,%d) is not in %s%s", order.BuildingType, order.Position.X, order.Position.Y, legal.name, legal.nearest(order.Position))
}

// siteList is one list of legal construction coordinates and the context field it
// comes from, so a rejection can name the list to choose from.
type siteList struct {
	name  string
	sites []controller.Coordinate
}

func (l siteList) has(site controller.Coordinate) bool { return slices.Contains(l.sites, site) }

// nearestSiteSuggestions bounds the alternatives a rejection lists.
const nearestSiteSuggestions = 3

// nearest suggests the legal sites closest to a rejected one, so a model can move the
// order instead of decoding the whole list again.
func (l siteList) nearest(site controller.Coordinate) string {
	if len(l.sites) == 0 {
		return "; none is left, so send []"
	}
	target := game.Position{X: site.X, Y: site.Y}
	distance := func(c controller.Coordinate) int { return game.HexDistance(game.Position{X: c.X, Y: c.Y}, target) }
	candidates := slices.Clone(l.sites)
	slices.SortStableFunc(candidates, func(a, b controller.Coordinate) int {
		return cmp.Or(cmp.Compare(distance(a), distance(b)), cmp.Compare(a.Y, b.Y), cmp.Compare(a.X, b.X))
	})
	suggestions := make([]string, 0, nearestSiteSuggestions)
	for _, candidate := range candidates[:min(len(candidates), nearestSiteSuggestions)] {
		suggestions = append(suggestions, fmt.Sprintf("(%d,%d)", candidate.X, candidate.Y))
	}
	return "; nearest legal: " + strings.Join(suggestions, ", ")
}

// Older observations without current counts report zero and are never rejected here.
func validateBuildingLimits(economy ai.EconomyContext, plan controller.PlanProposal) []ai.ValidationIssue {
	if plan.ConstructionOrders == nil {
		return nil
	}
	orders := *plan.ConstructionOrders
	economyOrders := 0
	for _, order := range orders {
		if order.BuildingType == game.BuildingEconomy {
			economyOrders++
		}
	}
	var issues []ai.ValidationIssue
	if remaining := economy.MaximumBuildings - economy.CurrentBuildings; economy.MaximumBuildings > 0 && len(orders) > remaining {
		issues = append(issues, ai.ValidationIssue{Field: fieldConstructionOrders, Code: "limit_reached", Message: fmt.Sprintf("%d construction orders exceed the %d remaining building slots (%d of %d used); submit at most %d or use []", len(orders), max(0, remaining), economy.CurrentBuildings, economy.MaximumBuildings, max(0, remaining))})
	}
	if remaining := economy.MaximumEconomyBuildings - economy.CurrentEconomyBuildings; economy.MaximumEconomyBuildings > 0 && economyOrders > remaining {
		issues = append(issues, ai.ValidationIssue{Field: fieldConstructionOrders, Code: "limit_reached", Message: fmt.Sprintf("%d economy orders exceed the %d remaining economy slots (%d of %d used)", economyOrders, max(0, remaining), economy.CurrentEconomyBuildings, economy.MaximumEconomyBuildings)})
	}
	return issues
}

// Production capabilities are checked against the frozen observation, not live
// state. Older stored contexts without authored rosters retain their old rules.
func validateTrainingSources(context ai.DecisionContext, plan controller.PlanProposal) []ai.ValidationIssue {
	specialized := false
	for _, b := range context.Legal.Buildings {
		specialized = specialized || len(b.Trains) > 0
	}
	for _, b := range context.View.State.Buildings {
		specialized = specialized || len(b.Trains) > 0
	}
	if !specialized || plan.RecruitmentOrders == nil {
		return nil
	}
	var issues []ai.ValidationIssue
	for _, order := range *plan.RecruitmentOrders {
		if !hasCompatibleTrainingSource(context, order) {
			issues = append(issues, ai.ValidationIssue{Field: fieldRecruitmentOrders, Code: "unsupported", Message: fmt.Sprintf("no legal source can train %q; choose an active building whose trains list includes this unit", order.UnitType)})
		}
	}
	return issues
}

func hasCompatibleTrainingSource(context ai.DecisionContext, order controller.RecruitmentOrder) bool {
	for _, b := range context.View.State.Buildings {
		matchesID := order.SourceBuildingID == "" || order.SourceBuildingID == b.ID
		if matchesID && slices.Contains(context.Legal.RecruitmentSources, b.ID) && slices.Contains(b.Trains, order.UnitType) {
			return true
		}
	}
	return false
}
