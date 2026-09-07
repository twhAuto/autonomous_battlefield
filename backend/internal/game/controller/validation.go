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
	if err := validateRecruitmentPriorities(proposal.UpgradeOrders); err != nil {
		return fmt.Errorf("upgrade orders: %w", err)
	}
	if err := validateRecruitmentOrders(proposal); err != nil {
		return err
	}
	if err := validateRecruitmentPriorities(proposal.RecruitmentPriorities); err != nil {
		return err
	}
	if err := validateConstructionPriorities(proposal.ConstructionPriorities); err != nil {
		return err
	}
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

func validateRecruitmentPriorities(priorities []string) error {
	seenRecruitment := make(map[string]bool, len(priorities))
	for _, unitType := range priorities {
		if strings.TrimSpace(unitType) == "" || seenRecruitment[unitType] {
			return fmt.Errorf("%w: recruitment priorities must be unique and non-empty", ErrInvalidPlan)
		}
		seenRecruitment[unitType] = true
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
