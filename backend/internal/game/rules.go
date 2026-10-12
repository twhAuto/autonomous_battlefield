// Validates rulesets and legacy faction blueprints, calculates design costs, and resolves
// unit and upgrade definitions.

package game

import (
	"fmt"
	"sort"
	"strings"
)

func ValidateRuleset(ruleset Ruleset) error {
	if err := ValidateKnowledge(ruleset); err != nil {
		return err
	}
	if !validRulesetMetadata(ruleset) {
		return fmt.Errorf("%w: ID, version, budget, and starting gold are invalid", ErrInvalidRuleset)
	}
	if len(ruleset.Units) == 0 {
		return fmt.Errorf("%w: units are required", ErrInvalidRuleset)
	}
	unitIDs := make(map[string]bool, len(ruleset.Units))
	for _, unit := range ruleset.Units {
		if !validUnitType(unit) || unitIDs[unit.ID] {
			return fmt.Errorf("%w: invalid or duplicate unit %q", ErrInvalidRuleset, unit.ID)
		}
		unitIDs[unit.ID] = true
	}
	if err := validateRulesetUpgrades(ruleset); err != nil {
		return err
	}
	if !validBuildingRules(ruleset.Buildings) {
		return fmt.Errorf("%w: invalid building rules", ErrInvalidRuleset)
	}
	if err := validateRulesetStructures(ruleset); err != nil {
		return err
	}
	if len(ruleset.Structures) > 0 && ruleset.Buildings.MaximumBuildingCount == 0 {
		return fmt.Errorf("%w: structures require a building limit", ErrInvalidRuleset)
	}
	if !validCombatRules(ruleset.Combat) {
		return fmt.Errorf("%w: invalid combat rules", ErrInvalidRuleset)
	}
	return nil
}

func validRulesetMetadata(ruleset Ruleset) bool {
	return strings.TrimSpace(ruleset.ID) != "" && strings.TrimSpace(ruleset.Version) != "" && ruleset.DesignBudget >= 0 && ruleset.StartingGold >= 0
}

func validUnitType(unit UnitType) bool {
	return (unit.AttackMode == "" || unit.AttackMode == "tracking" || unit.AttackMode == "position") && validUnitCapabilities(unit) && strings.TrimSpace(unit.ID) != "" && unit.Cost > 0 && unit.Health > 0 && unit.Attack >= 0 && unit.Defense >= 0 && unit.Speed >= 0 && unit.Movement > 0 && unit.Range > 0 && unit.Vision > 0
}

func validUpgrade(upgrade Upgrade) bool {
	return strings.TrimSpace(upgrade.ID) != "" && upgrade.Cost > 0 && upgrade.AttackBonus >= 0 && upgrade.DefenseBonus >= 0 && upgrade.EconomyIncomeBonus >= 0
}

func validBuildingRules(rules BuildingRules) bool {
	return rules.MaximumBuildingCount >= 0 && rules.MaximumBuildingCount <= 8 && rules.CastleHealth > 0 && rules.CastleDefense >= 0 && rules.CastleVision > 0 && rules.CastleIncome >= 0 && rules.EconomyCost > 0 && rules.EconomyHealth > 0 && rules.EconomyDefense >= 0 && rules.EconomyVision > 0 && rules.EconomyIncome >= 0 && rules.MaximumEconomyCount >= 0 && rules.MaximumEconomyCount <= 2
}

func validCombatRules(rules CombatRules) bool {
	return rules.MinimumDamage > 0 && percentage(rules.DefendAttackPercent) && percentage(rules.DefendDamageTakenPercent) && percentage(rules.LootPercent)
}

func percentage(value int64) bool { return value >= 0 && value <= 100 }

func ValidateBlueprint(ruleset Ruleset, blueprint Blueprint) (int64, error) {
	if err := ValidateRuleset(ruleset); err != nil {
		return 0, err
	}
	selected, totalUnits, unitCost, err := validateUnitSelections(UnitTypesByID(ruleset), blueprint.Units)
	if err != nil {
		return 0, err
	}
	if totalUnits == 0 || totalUnits > 6 {
		return 0, fmt.Errorf("%w: blueprint requires between one and six units", ErrInvalidBlueprint)
	}
	upgradeCost, err := validateUpgradeSelections(UpgradesByID(ruleset), blueprint.UpgradeIDs)
	if err != nil {
		return 0, err
	}
	cost := unitCost + upgradeCost
	if cost > ruleset.DesignBudget {
		return 0, fmt.Errorf("%w: design cost %d exceeds budget %d", ErrInvalidBlueprint, cost, ruleset.DesignBudget)
	}
	if err := validateRecruitmentPriority(selected, blueprint.RecruitmentPriority); err != nil {
		return 0, err
	}
	return cost, nil
}

func validateUnitSelections(unitTypes map[string]UnitType, selections []UnitSelection) (map[string]bool, int, int64, error) {
	selected := make(map[string]bool)
	totalUnits := 0
	cost := int64(0)
	for _, selection := range selections {
		definition, ok := unitTypes[selection.Type]
		if !ok || selected[selection.Type] || selection.Count <= 0 {
			return nil, 0, 0, fmt.Errorf("%w: invalid or duplicate unit selection %q", ErrInvalidBlueprint, selection.Type)
		}
		selected[selection.Type] = true
		totalUnits += selection.Count
		cost += definition.Cost * int64(selection.Count)
	}
	return selected, totalUnits, cost, nil
}

func validateUpgradeSelections(upgradeTypes map[string]Upgrade, ids []string) (int64, error) {
	upgrades := make(map[string]bool)
	cost := int64(0)
	for _, id := range ids {
		definition, ok := upgradeTypes[id]
		if !ok || upgrades[id] {
			return 0, fmt.Errorf("%w: invalid or duplicate upgrade %q", ErrInvalidBlueprint, id)
		}
		upgrades[id] = true
		cost += definition.Cost
	}
	return cost, nil
}

func validateRecruitmentPriority(selected map[string]bool, recruitmentPriority []string) error {
	if len(recruitmentPriority) != len(selected) {
		return fmt.Errorf("%w: recruitment priority must contain each selected unit type once", ErrInvalidBlueprint)
	}
	priority := make(map[string]bool)
	for _, id := range recruitmentPriority {
		if !selected[id] || priority[id] {
			return fmt.Errorf("%w: invalid recruitment priority %q", ErrInvalidBlueprint, id)
		}
		priority[id] = true
	}
	return nil
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

func validUnitCapabilities(u UnitType) bool {
	validClass := func(c string) bool {
		return c == "" || c == "infantry" || c == "mobile" || c == "siege" || c == "support"
	}
	return validClass(u.Class) && validClass(u.BonusAgainstClass) && u.MinimumRange >= 0 && u.MinimumRange <= u.Range && u.HealAmount >= 0 && u.BuildingDamageBonus >= 0 && u.ClassDamageBonus >= 0 && (u.ClassDamageBonus == 0 || u.BonusAgainstClass != "") && (u.HealAmount == 0 || u.Attack == 0 && u.Class == "support" && u.Range == 1 && u.MinimumRange == 0 && !u.BuildingPriority && u.BuildingDamageBonus == 0 && u.ClassDamageBonus == 0)
}

// ValidStructure accepts the additional constructible building capabilities.
func ValidStructure(b BuildingType) bool {
	if b.ID != b.Kind || b.Health <= 0 || b.Defense < 0 || b.Vision <= 0 || b.Cost <= 0 || b.Income != 0 {
		return false
	}
	if b.Kind == BuildingTower {
		return b.Attack > 0 && b.Range > 0
	}
	return (b.Kind == BuildingWatchtower || b.Kind == BuildingBarracks) && b.Attack == 0 && b.Range == 0
}

func validateRulesetUpgrades(ruleset Ruleset) error {
	upgradeIDs := make(map[string]bool, len(ruleset.Upgrades))
	for _, upgrade := range ruleset.Upgrades {
		if !validUpgrade(upgrade) || upgradeIDs[upgrade.ID] {
			return fmt.Errorf("%w: invalid or duplicate upgrade %q", ErrInvalidRuleset, upgrade.ID)
		}
		upgradeIDs[upgrade.ID] = true
	}
	return nil
}

func validateRulesetStructures(ruleset Ruleset) error {
	seenBuildings := map[string]bool{}
	for _, b := range ruleset.Structures {
		if !ValidStructure(b) || seenBuildings[b.ID] {
			return fmt.Errorf("%w: invalid or duplicate structure %q", ErrInvalidRuleset, b.ID)
		}
		seenBuildings[b.ID] = true
	}
	return nil
}
