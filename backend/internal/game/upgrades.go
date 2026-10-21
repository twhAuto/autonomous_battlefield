// Applies affordable upgrade purchases in plan order and updates the faction's existing
// units and economy bonuses.

package game

import (
	"errors"
	"slices"

	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
)

func (m *Match) runUpgrades(id string, plan controller.PlanProposal) []Event {
	events := []Event{}
	state := m.factionState(id)
	definitions := UpgradesByID(m.scenario.Ruleset)
	for _, upgradeID := range plan.UpgradeOrders {
		if slices.Contains(state.OwnedUpgradeIDs, upgradeID) {
			continue
		}
		upgrade, ok := definitions[upgradeID]
		if !ok {
			continue
		} // Plan validation rejects unknown IDs before mutation.
		if err := ValidateUpgradeOrder(m.scenario.Ruleset.Upgrades, state.OwnedUpgradeIDs, []string{upgradeID}); err != nil {
			reason := "prerequisite_required"
			if errors.Is(err, errUpgradeChoiceConflict) {
				reason = "exclusive_choice_owned"
			}
			events = append(events, m.event("production_skipped", map[string]any{"factionId": id, "kind": "upgrades", "upgradeId": upgradeID, "reason": reason}))
			break
		}
		if state.Gold-upgrade.Cost < plan.ReserveGold {
			events = append(events, m.event("production_skipped", map[string]any{"factionId": id, "kind": "upgrades", "upgradeId": upgradeID, "reason": "reserve_required"}))
			break
		}
		events = append(events, m.purchaseUpgrade(id, upgrade))
	}
	return events
}

func applyUnitUpgrade(unit *Unit, upgrade Upgrade) {
	if upgrade.TargetUnit != "" && unit.Type != upgrade.TargetUnit || upgrade.TargetClass != "" && unit.Class != upgrade.TargetClass {
		return
	}
	if unit.HealAmount == 0 && unit.RepairAmount == 0 {
		unit.Attack += upgrade.AttackBonus
	}
	unit.Defense += upgrade.DefenseBonus
	unit.Movement += upgrade.MovementBonus
	unit.Range += upgrade.RangeBonus
	unit.BuildingDamageBonus += upgrade.BuildingDamageBonus
	if unit.HealAmount > 0 {
		unit.HealAmount += upgrade.HealAmountBonus
		if upgrade.HealRangeBonus > 0 {
			unit.HealRange = max(1, unit.HealRange) + upgrade.HealRangeBonus
		}
	}
}

func (m *Match) applyUpgradeToEntities(id string, upgrade Upgrade) {
	for i := range m.state.Units {
		unit := &m.state.Units[i]
		if unit.FactionID == id && unit.Health > 0 {
			applyUnitUpgrade(unit, upgrade)
		}
	}
	for i := range m.state.Buildings {
		building := &m.state.Buildings[i]
		if building.FactionID == id && building.Health > 0 && building.Type == BuildingEconomy {
			building.Income += upgrade.EconomyIncomeBonus
		}
	}

}

func (m *Match) purchaseUpgrade(id string, upgrade Upgrade) Event {
	state, runtime := m.factionState(id), m.factions[id]
	state.Gold -= upgrade.Cost
	state.GoldSpent += upgrade.Cost
	state.OwnedUpgradeIDs = append(state.OwnedUpgradeIDs, upgrade.ID)
	if upgrade.TargetUnit != "" || upgrade.TargetClass != "" {
		runtime.scopedUpgrades = append(runtime.scopedUpgrades, upgrade)
	} else {
		runtime.attackBonus += upgrade.AttackBonus
		runtime.defenseBonus += upgrade.DefenseBonus
		runtime.economyBonus += upgrade.EconomyIncomeBonus
	}
	m.applyUpgradeToEntities(id, upgrade)
	data := map[string]any{"factionId": id, "upgradeId": upgrade.ID, "cost": upgrade.Cost, "balance": state.Gold}
	if upgrade.Level > 0 {
		data["track"], data["level"] = upgrade.Track, upgrade.Level
		data["attackBonus"], data["defenseBonus"], data["economyIncomeBonus"] = upgrade.AttackBonus, upgrade.DefenseBonus, upgrade.EconomyIncomeBonus
	}
	return m.event("upgrade_purchased", data)
}
