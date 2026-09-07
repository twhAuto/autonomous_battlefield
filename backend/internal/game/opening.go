// Applies the tick-zero opening decision and its deterministic fallback before normal
// combat ticks begin.

package game

import (
	"fmt"
	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
	"sort"
)

func (m *Match) OpeningDue() bool {
	return !m.finished && m.scenario.SnapshotVersion >= DoctrineSnapshotVersion && !m.state.OpeningComplete
}

// OpeningFallback deliberately ignores doctrine: scripted controllers cannot interpret prose.
func (m *Match) OpeningFallback(factionID string) controller.PlanProposal {
	units := append([]UnitType(nil), m.scenario.Ruleset.Units...)
	sort.Slice(units, func(i, j int) bool { return units[i].ID < units[j].ID })
	gold := m.factionState(factionID).Gold
	orders := []controller.RecruitmentOrder{}
	for len(orders) < 6 {
		purchased := false
		for _, unit := range units {
			if len(orders) == 6 {
				break
			}
			if unit.HealAmount == 0 && unit.Cost <= gold {
				orders = append(orders, controller.RecruitmentOrder{UnitType: unit.ID, Count: 1})
				gold -= unit.Cost
				purchased = true
			}
		}
		if !purchased {
			break
		}
	}
	return controller.PlanProposal{Name: "balanced-opening", ActionWeights: map[string]int64{"attack": 1000, "defend": 1000}, SpendOrder: []string{"recruitment"}, RecruitmentOrders: &orders}
}

func (m *Match) advanceOpening(plans map[string]controller.PlanProposal) (Transition, error) {
	resolved := make(map[string]controller.PlanProposal, len(m.factions))
	for _, id := range m.sortedFactionIDs() {
		resolved[id] = m.OpeningFallback(id)
	}
	for id, plan := range plans {
		if err := validateOpeningPlan(plan); err != nil {
			return Transition{}, err
		}
		resolved[id] = plan
	}
	if err := m.applyPlans(resolved); err != nil {
		return Transition{}, err
	}
	events := m.startEvents()
	for _, id := range m.sortedFactionIDs() {
		plan := m.factions[id].activePlan
		events = append(events, m.event("plan_applied", map[string]any{"factionId": id, "name": plan.Name, "targetTick": uint64(0)}))
		for _, kind := range plan.SpendOrder {
			switch kind {
			case "upgrades":
				events = append(events, m.runUpgrades(id, plan)...)
			case "recruitment":
				events = append(events, m.runRecruitment(id, plan)...)
			}
		}
		state := m.factionState(id)
		state.UnitsStarted = state.UnitsRecruited
		state.UnitsRecruited = 0
	}
	m.state.OpeningComplete = true
	events = append(events, m.event("opening_completed", map[string]any{}))
	m.sortState()
	return m.transition(events), nil
}

func validateOpeningPlan(plan controller.PlanProposal) error {
	if len(plan.ConstructionPriorities) > 0 || plan.ConstructionOrders != nil && len(*plan.ConstructionOrders) > 0 {
		return fmt.Errorf("construction is unavailable during opening")
	}
	for _, kind := range plan.SpendOrder {
		if kind == "construction" {
			return fmt.Errorf("construction is unavailable during opening")
		}
	}
	if plan.RecruitmentOrders == nil {
		return fmt.Errorf("opening requires explicit recruitment orders")
	}
	return nil
}
