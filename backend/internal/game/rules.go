package game

import (
	"fmt"
	"sort"
	"strings"
)

func ValidateRuleset(ruleset Ruleset) error {
	if strings.TrimSpace(ruleset.ID) == "" || strings.TrimSpace(ruleset.Version) == "" || ruleset.DesignBudget <= 0 || ruleset.StartingGold < 0 {
		return fmt.Errorf("%w: ID, version, budget, and starting gold are invalid", ErrInvalidRuleset)
	}
	if len(ruleset.Units) == 0 || len(ruleset.Upgrades) == 0 {
		return fmt.Errorf("%w: units and upgrades are required", ErrInvalidRuleset)
	}
	unitIDs := make(map[string]bool, len(ruleset.Units))
	for _, unit := range ruleset.Units {
		if strings.TrimSpace(unit.ID) == "" || unitIDs[unit.ID] || unit.Cost <= 0 || unit.Health <= 0 || unit.Attack < 0 || unit.Defense < 0 || unit.Speed < 0 || unit.Movement <= 0 || unit.Range <= 0 || unit.Vision <= 0 {
			return fmt.Errorf("%w: invalid or duplicate unit %q", ErrInvalidRuleset, unit.ID)
		}
		unitIDs[unit.ID] = true
	}
	upgradeIDs := make(map[string]bool, len(ruleset.Upgrades))
	for _, upgrade := range ruleset.Upgrades {
		if strings.TrimSpace(upgrade.ID) == "" || upgradeIDs[upgrade.ID] || upgrade.Cost <= 0 || upgrade.AttackBonus < 0 || upgrade.DefenseBonus < 0 || upgrade.EconomyIncomeBonus < 0 {
			return fmt.Errorf("%w: invalid or duplicate upgrade %q", ErrInvalidRuleset, upgrade.ID)
		}
		upgradeIDs[upgrade.ID] = true
	}
	b := ruleset.Buildings
	if b.CastleHealth <= 0 || b.CastleDefense < 0 || b.CastleVision <= 0 || b.CastleIncome < 0 || b.EconomyCost <= 0 || b.EconomyHealth <= 0 || b.EconomyDefense < 0 || b.EconomyVision <= 0 || b.EconomyIncome < 0 || b.MaximumEconomyCount != 2 {
		return fmt.Errorf("%w: invalid building rules", ErrInvalidRuleset)
	}
	c := ruleset.Combat
	if c.MinimumDamage <= 0 || c.DefendAttackPercent < 0 || c.DefendAttackPercent > 100 || c.DefendDamageTakenPercent < 0 || c.DefendDamageTakenPercent > 100 || c.LootPercent < 0 || c.LootPercent > 100 {
		return fmt.Errorf("%w: invalid combat rules", ErrInvalidRuleset)
	}
	return nil
}

func ValidateBlueprint(ruleset Ruleset, blueprint Blueprint) (int64, error) {
	if err := ValidateRuleset(ruleset); err != nil {
		return 0, err
	}
	unitTypes := UnitTypesByID(ruleset)
	upgradeTypes := UpgradesByID(ruleset)
	selected := make(map[string]bool)
	totalUnits := 0
	cost := int64(0)
	for _, selection := range blueprint.Units {
		definition, ok := unitTypes[selection.Type]
		if !ok || selected[selection.Type] || selection.Count <= 0 {
			return 0, fmt.Errorf("%w: invalid or duplicate unit selection %q", ErrInvalidBlueprint, selection.Type)
		}
		selected[selection.Type] = true
		totalUnits += selection.Count
		cost += definition.Cost * int64(selection.Count)
	}
	if totalUnits == 0 || totalUnits > 6 {
		return 0, fmt.Errorf("%w: blueprint requires between one and six units", ErrInvalidBlueprint)
	}
	upgrades := make(map[string]bool)
	for _, id := range blueprint.UpgradeIDs {
		definition, ok := upgradeTypes[id]
		if !ok || upgrades[id] {
			return 0, fmt.Errorf("%w: invalid or duplicate upgrade %q", ErrInvalidBlueprint, id)
		}
		upgrades[id] = true
		cost += definition.Cost
	}
	if cost > ruleset.DesignBudget {
		return 0, fmt.Errorf("%w: design cost %d exceeds budget %d", ErrInvalidBlueprint, cost, ruleset.DesignBudget)
	}
	if len(blueprint.RecruitmentPriority) != len(selected) {
		return 0, fmt.Errorf("%w: recruitment priority must contain each selected unit type once", ErrInvalidBlueprint)
	}
	priority := make(map[string]bool)
	for _, id := range blueprint.RecruitmentPriority {
		if !selected[id] || priority[id] {
			return 0, fmt.Errorf("%w: invalid recruitment priority %q", ErrInvalidBlueprint, id)
		}
		priority[id] = true
	}
	return cost, nil
}

func UnitTypesByID(ruleset Ruleset) map[string]UnitType {
	result := make(map[string]UnitType, len(ruleset.Units))
	for _, definition := range ruleset.Units {
		result[definition.ID] = definition
	}
	return result
}

func UpgradesByID(ruleset Ruleset) map[string]Upgrade {
	result := make(map[string]Upgrade, len(ruleset.Upgrades))
	for _, definition := range ruleset.Upgrades {
		result[definition.ID] = definition
	}
	return result
}

func DesignCost(ruleset Ruleset, blueprint Blueprint) int64 {
	cost, _ := ValidateBlueprint(ruleset, blueprint)
	return cost
}

func normalizedSelections(blueprint Blueprint) []UnitSelection {
	result := append([]UnitSelection(nil), blueprint.Units...)
	sort.Slice(result, func(i, j int) bool { return result[i].Type < result[j].Type })
	return result
}

func upgradeBonuses(ruleset Ruleset, blueprint Blueprint) (attack, defense, economy int64) {
	definitions := UpgradesByID(ruleset)
	for _, id := range blueprint.UpgradeIDs {
		upgrade := definitions[id]
		attack += upgrade.AttackBonus
		defense += upgrade.DefenseBonus
		economy += upgrade.EconomyIncomeBonus
	}
	return attack, defense, economy
}

func CloneBlueprint(value Blueprint) Blueprint {
	return Blueprint{
		Units:               append([]UnitSelection(nil), value.Units...),
		UpgradeIDs:          append([]string(nil), value.UpgradeIDs...),
		RecruitmentPriority: append([]string(nil), value.RecruitmentPriority...),
	}
}
