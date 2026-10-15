// Applies affordable upgrade purchases in plan order and updates the faction's existing
// units and economy bonuses.

package game

import (
	"slices"

	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
)

func (m *Match) runUpgrades(id string, plan controller.PlanProposal) []Event {
	events := []Event{}
	state, runtime := m.factionState(id), m.factions[id]
	definitions := UpgradesByID(m.scenario.Ruleset)
	for _, upgradeID := range plan.UpgradeOrders {
		if slices.Contains(state.OwnedUpgradeIDs, upgradeID) {
			continue
		}
		upgrade, ok := definitions[upgradeID]
		if !ok {
			continue
		} // Plan validation rejects unknown IDs before mutation.
		if state.Gold-upgrade.Cost < plan.ReserveGold {
			events = append(events, m.event("production_skipped", map[string]any{"factionId": id, "kind": "upgrades", "upgradeId": upgradeID, "reason": "reserve_required"}))
			break
		}
		state.Gold -= upgrade.Cost
		state.GoldSpent += upgrade.Cost
		state.OwnedUpgradeIDs = append(state.OwnedUpgradeIDs, upgradeID)
		runtime.attackBonus += upgrade.AttackBonus
		runtime.defenseBonus += upgrade.DefenseBonus
		runtime.economyBonus += upgrade.EconomyIncomeBonus
		m.applyUpgradeToEntities(id, upgrade)
		events = append(events, m.event("upgrade_purchased", map[string]any{"factionId": id, "upgradeId": upgradeID, "cost": upgrade.Cost, "balance": state.Gold}))
	}
	return events
}

func (m *Match) applyUpgradeToEntities(id string, upgrade Upgrade) {
	for i := range m.state.Units {
		unit := &m.state.Units[i]
		if unit.FactionID == id && unit.Health > 0 {
			if unit.HealAmount == 0 {
				unit.Attack += upgrade.AttackBonus
			}
			unit.Defense += upgrade.DefenseBonus
		}
	}
	for i := range m.state.Buildings {
		building := &m.state.Buildings[i]
		if building.FactionID == id && building.Health > 0 && building.Type == BuildingEconomy {
			building.Income += upgrade.EconomyIncomeBonus
		}
	}

}
