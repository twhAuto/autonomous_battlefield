package game

import "fmt"

func validSpecialist(u UnitType) bool {
	if u.ArmorPiercing < 0 || u.RepairAmount < 0 || u.RepairCost < 0 || u.RepairCharges < 0 || u.HealRange < 0 {
		return false
	}
	if u.RepairAmount > 0 {
		return u.Class == "support" && u.Attack == 0 && u.HealAmount == 0 && u.RepairCost > 0 && u.RepairCharges > 0 && !u.Scout && u.ArmorPiercing == 0 && u.PreferredTargetClass == "" && u.HealRange == 0 && !u.BuildingPriority && u.BuildingDamageBonus == 0 && u.ClassDamageBonus == 0 && u.BonusAgainstClass == "" && u.MinimumRange == 0
	}
	return u.RepairCost == 0 && u.RepairCharges == 0 && (u.HealRange == 0 || u.HealAmount > 0)
}

func validateUpgradeEffects(r Ruleset) error {
	for _, u := range r.Upgrades {
		scoped := u.TargetUnit != "" || u.TargetClass != ""
		if u.TargetUnit != "" && u.TargetClass != "" || u.MovementBonus < 0 || u.RangeBonus < 0 || u.HealAmountBonus < 0 || u.HealRangeBonus < 0 || u.BuildingDamageBonus < 0 {
			return fmt.Errorf("%w: invalid scoped upgrade %q", ErrInvalidRuleset, u.ID)
		}
		extra := u.MovementBonus > 0 || u.RangeBonus > 0 || u.HealAmountBonus > 0 || u.HealRangeBonus > 0 || u.BuildingDamageBonus > 0
		if !scoped && (extra || u.ExclusiveGroup != "") || scoped && u.EconomyIncomeBonus != 0 {
			return fmt.Errorf("%w: upgrade %q has incompatible scope", ErrInvalidRuleset, u.ID)
		}
		if !scoped {
			continue
		}
		matched := false
		for _, unit := range r.Units {
			if u.TargetUnit != "" && unit.ID != u.TargetUnit || u.TargetClass != "" && unit.Class != u.TargetClass {
				continue
			}
			matched = true
			if (u.HealAmountBonus > 0 || u.HealRangeBonus > 0) && unit.HealAmount == 0 {
				return fmt.Errorf("%w: healing upgrade requires a healer", ErrInvalidRuleset)
			}
		}
		if !matched {
			return fmt.Errorf("%w: upgrade %q has no matching unit", ErrInvalidRuleset, u.ID)
		}
	}
	return nil
}
