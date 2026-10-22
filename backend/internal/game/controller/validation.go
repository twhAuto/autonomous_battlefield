// Validates controller weights, priorities, construction coordinates, and explicit
// recruitment orders before plans are applied.

package controller

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

func validateProposal(proposal PlanProposal, actionKinds map[string]struct{}) error {
	if strings.TrimSpace(proposal.Name) == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidPlan)
	}
	if utf8.RuneCountInString(strings.TrimSpace(proposal.Rationale)) > maxRationaleCharacters {
		return fmt.Errorf("%w: rationale exceeds %d characters", ErrInvalidPlan, maxRationaleCharacters)
	}
	if proposal.ReserveGold < 0 {
		return fmt.Errorf("%w: reserve gold cannot be negative", ErrInvalidPlan)
	}
	if err := validateActionWeights(proposal.ActionWeights, actionKinds); err != nil {
		return err
	}
	if err := validateTargetBonuses(proposal.TargetBonuses); err != nil {
		return err
	}
	if err := validateSpendOrder(proposal.SpendOrder); err != nil {
		return err
	}
	if err := validateUniqueNames("upgrade orders", proposal.UpgradeOrders); err != nil {
		return err
	}
	if err := validateRecruitmentOrders(proposal); err != nil {
		return err
	}
	if err := validateUniqueNames("recruitment priorities", proposal.RecruitmentPriorities); err != nil {
		return err
	}
	if err := validateConstructionPriorities(proposal.ConstructionPriorities); err != nil {
		return err
	}
	if err := validateProductionOrders(proposal); err != nil {
		return err
	}
	return validateTypedConstruction(proposal)
}

func validateActionWeights(weights map[string]int64, actionKinds map[string]struct{}) error {
	for kind, weight := range weights {
		if _, ok := actionKinds[kind]; !ok {
			return fmt.Errorf("%w: unsupported action kind %q", ErrInvalidPlan, kind)
		}
		if weight < 0 || weight > maxWeight {
			return fmt.Errorf("%w: action weight for %q is outside [0, %d]", ErrInvalidPlan, kind, maxWeight)
		}
	}
	return nil
}

func validateTargetBonuses(bonuses map[string]int64) error {
	for target, bonus := range bonuses {
		if strings.TrimSpace(target) == "" {
			return fmt.Errorf("%w: target bonus key is required", ErrInvalidPlan)
		}
		if bonus < -maxTargetBonus || bonus > maxTargetBonus {
			return fmt.Errorf("%w: target bonus for %q is outside [%d, %d]", ErrInvalidPlan, target, -maxTargetBonus, maxTargetBonus)
		}
	}
	return nil
}

func validateSpendOrder(spendOrder []string) error {
	seenSpend := make(map[string]bool, len(spendOrder))
	for _, kind := range spendOrder {
		if kind != "construction" && kind != "recruitment" && kind != "upgrades" {
			return fmt.Errorf("%w: unsupported spend kind %q", ErrInvalidPlan, kind)
		}
		if seenSpend[kind] {
			return fmt.Errorf("%w: duplicate spend kind %q", ErrInvalidPlan, kind)
		}
		seenSpend[kind] = true
	}
	return nil
}

// validateUniqueNames rejects empty or repeated entries in the named list.
func validateUniqueNames(field string, names []string) error {
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if strings.TrimSpace(name) == "" || seen[name] {
			return fmt.Errorf("%w: %s must be unique and non-empty", ErrInvalidPlan, field)
		}
		seen[name] = true
	}
	return nil
}

func validateConstructionPriorities(priorities []Coordinate) error {
	seenSites := make(map[Coordinate]bool, len(priorities))
	for _, site := range priorities {
		if seenSites[site] {
			return fmt.Errorf("%w: construction priorities must be unique", ErrInvalidPlan)
		}
		seenSites[site] = true
	}
	return nil
}

// ValidatePlanProposal validates a data-driven plan against the action kinds
// supported by a ruleset. It lets scenario loaders reject invalid plans before
// a battle is queued.
func ValidatePlanProposal(proposal PlanProposal, actionKinds []string) error {
	allowed := make(map[string]struct{}, len(actionKinds))
	for _, kind := range actionKinds {
		if strings.TrimSpace(kind) == "" {
			return fmt.Errorf("%w: action kinds cannot be empty", ErrInvalidPlan)
		}
		if _, duplicate := allowed[kind]; duplicate {
			return fmt.Errorf("%w: duplicate action kind %q", ErrInvalidPlan, kind)
		}
		allowed[kind] = struct{}{}
	}
	if len(allowed) == 0 {
		return fmt.Errorf("%w: at least one action kind is required", ErrInvalidPlan)
	}
	return validateProposal(proposal, allowed)
}

func validateRecruitmentOrders(plan PlanProposal) error {
	if plan.RecruitmentOrders == nil {
		return nil
	}
	if len(plan.RecruitmentPriorities) > 0 {
		return fmt.Errorf("%w: use recruitmentOrders or recruitmentPriorities, not both", ErrInvalidPlan)
	}
	if len(*plan.RecruitmentOrders) > 6 {
		return fmt.Errorf("%w: at most six recruitment orders per window", ErrInvalidPlan)
	}
	total := 0
	for _, order := range *plan.RecruitmentOrders {
		if strings.TrimSpace(order.UnitType) == "" || order.Count < 1 || order.Count > 6 {
			return fmt.Errorf("%w: recruitment orders need a unitType and count from 1 to 6", ErrInvalidPlan)
		}
		total += order.Count
	}
	if total > 6 {
		return fmt.Errorf("%w: at most six requested units per window", ErrInvalidPlan)
	}
	return nil
}

func validateTypedConstruction(proposal PlanProposal) error {
	if proposal.ConstructionOrders != nil {
		if len(proposal.ConstructionPriorities) > 0 || len(*proposal.ConstructionOrders) > 8 {
			return fmt.Errorf("%w: use at most eight typed construction orders without legacy priorities", ErrInvalidPlan)
		}
		sites := []Coordinate{}
		for _, order := range *proposal.ConstructionOrders {
			if strings.TrimSpace(order.BuildingType) == "" {
				return fmt.Errorf("%w: buildingType is required", ErrInvalidPlan)
			}
			sites = append(sites, order.Position)
		}
		return validateConstructionPriorities(sites)
	}
	return nil

}

func validateProductionOrders(plan PlanProposal) error {
	if plan.ProductionOrders == nil {
		return nil
	}
	if len(*plan.ProductionOrders) > 9 {
		return fmt.Errorf("%w: at most nine building production orders", ErrInvalidPlan)
	}
	seen := map[string]bool{}
	for _, order := range *plan.ProductionOrders {
		if strings.TrimSpace(order.BuildingID) == "" || seen[order.BuildingID] {
			return fmt.Errorf("%w: production building IDs must be unique and non-empty", ErrInvalidPlan)
		}
		if order.Enabled && strings.TrimSpace(order.UnitType) == "" {
			return fmt.Errorf("%w: enabled production requires unitType", ErrInvalidPlan)
		}
		seen[order.BuildingID] = true
	}
	return nil
}
