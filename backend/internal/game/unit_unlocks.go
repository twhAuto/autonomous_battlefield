// Validates and enforces unit types that stay locked until a faction owns an
// unlocking upgrade.

package game

import (
	"fmt"
	"slices"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/game/controller"
)

func validateUnitUnlocks(r Ruleset) error {
	upgrades := UpgradesByID(r)
	for _, unit := range r.Units {
		if unit.RequiresUpgradeID == "" {
			continue
		}
		if _, ok := upgrades[unit.RequiresUpgradeID]; !ok {
			return fmt.Errorf("%w: unit %q requires unknown upgrade %q", ErrInvalidRuleset, unit.ID, unit.RequiresUpgradeID)
		}
	}
	return nil
}

// UnitUnlocked reports whether owned upgrades permit training the unit type.
func UnitUnlocked(unit UnitType, owned []string) bool {
	return unit.RequiresUpgradeID == "" || slices.Contains(owned, unit.RequiresUpgradeID)
}

// ValidateUnitUnlocks rejects plans that select a locked unit type unless the
// unlocking upgrade is owned or ordered in the same plan. Like ValidateUpgradeOrder,
// it does not simulate affordability: production of a type whose unlock was
// ordered but not bought waits until the upgrade is owned.
func ValidateUnitUnlocks(units []UnitType, owned []string, plan controller.PlanProposal) error {
	definitions := UnitTypesByID(Ruleset{Units: units})
	available := append(slices.Clone(owned), plan.UpgradeOrders...)
	for _, id := range plannedUnitTypes(plan) {
		if unit, ok := definitions[id]; ok && !UnitUnlocked(unit, available) {
			return fmt.Errorf("%w: unit %q requires upgrade %q owned or ordered in upgradeOrders", controller.ErrInvalidPlan, id, unit.RequiresUpgradeID)
		}
	}
	return nil
}

func plannedUnitTypes(plan controller.PlanProposal) []string {
	ids := append([]string{}, plan.RecruitmentPriorities...)
	if plan.RecruitmentOrders != nil {
		for _, order := range *plan.RecruitmentOrders {
			ids = append(ids, order.UnitType)
		}
	}
	if plan.ProductionOrders != nil {
		for _, order := range *plan.ProductionOrders {
			if order.UnitType != "" {
				ids = append(ids, order.UnitType)
			}
		}
	}
	return ids
}

func (m *Match) unitLocked(factionID, unitType string) bool {
	unit, ok := UnitTypesByID(m.scenario.Ruleset)[unitType]
	return ok && !UnitUnlocked(unit, m.factionState(factionID).OwnedUpgradeIDs)
}

func (m *Match) unlockedUnits(factionID string, ids []string) []string {
	return slices.DeleteFunc(slices.Clone(ids), func(id string) bool { return m.unitLocked(factionID, id) })
}

// scriptedSourceUnit returns the type a scripted plan selects for a source from
// its unlocked preferences, or "" to leave the source unchanged.
func (m *Match) scriptedSourceUnit(factionID string, b *Building, preferred []string) string {
	choices := m.trainablePriorities(b.Type, preferred)
	if b.Production != nil && b.Production.Configured {
		return m.lateGameReplacement(*b.Production, choices)
	}
	if len(choices) > 0 {
		return choices[0]
	}
	if roster := m.unlockedUnits(factionID, m.trainingRoster(b.Type)); len(roster) > 0 {
		return roster[0]
	}
	return ""
}

// lateGameReplacement moves an enabled source off a type that needs no unlock to
// the first unlocked choice that does. Disabled and late-game sources are kept.
func (m *Match) lateGameReplacement(production BuildingProduction, choices []string) string {
	units := UnitTypesByID(m.scenario.Ruleset)
	if !production.Enabled || units[production.UnitType].RequiresUpgradeID != "" {
		return ""
	}
	for _, id := range choices {
		if units[id].RequiresUpgradeID != "" {
			return id
		}
	}
	return ""
}
