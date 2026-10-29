package game

import (
	"fmt"
	"slices"

	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
)

func (m *Match) initializeProduction(b *Building) {
	if m.scenario.Ruleset.ContinuousProduction && len(b.Trains) > 0 {
		b.Production = &BuildingProduction{}
	}
}

// ValidateBuildingProduction is shared by the engine and the frozen AI validator.
// It never changes the state and permits an off order without a new unit selection.
func ValidateBuildingProduction(continuous bool, factionID string, state State, sources []string, plan controller.PlanProposal) error {
	if !continuous {
		if plan.ProductionOrders != nil {
			return fmt.Errorf("%w: productionOrders require continuous production", controller.ErrInvalidPlan)
		}
		return nil
	}
	if len(plan.RecruitmentPriorities) > 0 || plan.RecruitmentOrders != nil && len(*plan.RecruitmentOrders) > 0 {
		return fmt.Errorf("%w: use productionOrders to select a unit per building", controller.ErrInvalidPlan)
	}
	if plan.ProductionOrders == nil {
		return nil
	}
	for _, order := range *plan.ProductionOrders {
		if !validProductionSource(factionID, state, sources, order) {
			return fmt.Errorf("%w: production building %q must be an active owned source with a compatible unit", controller.ErrInvalidPlan, order.BuildingID)
		}
	}
	return nil
}

func validProductionSource(factionID string, state State, sources []string, order controller.ProductionOrder) bool {
	if !slices.Contains(sources, order.BuildingID) {
		return false
	}
	for _, b := range state.Buildings {
		if b.ID == order.BuildingID && b.FactionID == factionID && b.Health > 0 {
			return order.UnitType == "" && !order.Enabled || slices.Contains(b.Trains, order.UnitType)
		}
	}
	return false
}

func (m *Match) applyBuildingProduction(plan controller.PlanProposal) {
	if plan.ProductionOrders == nil {
		return
	}
	for _, order := range *plan.ProductionOrders {
		for i := range m.state.Buildings {
			b := &m.state.Buildings[i]
			if b.ID != order.BuildingID {
				continue
			}
			if b.Production == nil {
				b.Production = &BuildingProduction{}
			}
			if order.UnitType != "" {
				b.Production.UnitType = order.UnitType
			}
			b.Production.Configured = true
			b.Production.Enabled = order.Enabled
			b.Production.WaitingForGold = false
			b.Production.WaitingForUpgrade = false
		}
	}
}

// Purchases happen after income, construction/upgrades, and completed deployments.
// A blocked exit retains the single paid unit and cannot incur another charge.
func (m *Match) runContinuousProduction() []Event {
	if !m.scenario.Ruleset.ContinuousProduction {
		return nil
	}
	indices := make([]int, len(m.state.Buildings))
	for i := range indices {
		indices[i] = i
	}
	m.sortBuildings(indices)
	definitions := UnitTypesByID(m.scenario.Ruleset)
	events := []Event{}
	for _, i := range indices {
		b := &m.state.Buildings[i]
		if event, bought := m.startContinuousUnit(b, definitions); bought {
			events = append(events, event)
		}
	}
	return events
}

func (m *Match) startContinuousUnit(b *Building, definitions map[string]UnitType) (Event, bool) {
	if b.Production == nil {
		return Event{}, false
	}
	b.Production.WaitingForGold = false
	b.Production.WaitingForUnitLimit = false
	b.Production.WaitingForUpgrade = false
	castle := m.castle(b.FactionID)
	if !b.Production.Enabled || b.Health <= 0 || b.ReadyAtTick > m.state.Tick || len(b.TrainingQueue) > 0 || castle == nil || castle.Health <= 0 {
		return Event{}, false
	}
	if m.unitLocked(b.FactionID, b.Production.UnitType) {
		b.Production.WaitingForUpgrade = true
		return Event{}, false
	}
	if m.atUnitLimit(b.FactionID) {
		b.Production.WaitingForUnitLimit = true
		return Event{}, false
	}
	reserve := m.factions[b.FactionID].activePlan.ReserveGold
	if affordableUnit(definitions, []string{b.Production.UnitType}, m.factionState(b.FactionID).Gold, reserve).ID == "" {
		b.Production.WaitingForGold = true
		return Event{}, false
	}
	event, bought, _ := m.attemptRecruitment(b.FactionID, b, definitions, []string{b.Production.UnitType}, reserve)
	b.Production.WaitingForGold = !bought
	return event, bought
}

// Only engine-authored templates are converted; submitted legacy purchases are
// rejected in continuous mode. A new source selects its first unlocked preferred
// type. A disabled source stays unchanged, and a configured one changes only to a
// preferred late-game type its faction has since unlocked. Model factions receive
// these plans only as first-window fallbacks, before they can own an unlock.
func (m *Match) scriptedProductionPlan(factionID string, plan controller.PlanProposal) controller.PlanProposal {
	preferred := append([]string{}, plan.RecruitmentPriorities...)
	if plan.RecruitmentOrders != nil {
		for _, order := range *plan.RecruitmentOrders {
			preferred = append(preferred, order.UnitType)
		}
	}
	preferred = m.unlockedUnits(factionID, preferred)
	orders := []controller.ProductionOrder{}
	for _, id := range m.recruitmentSources(factionID) {
		if len(orders) == controller.MaximumProductionOrders {
			break
		}
		if unit := m.scriptedSourceUnit(factionID, m.recruitmentSource(factionID, id), preferred); unit != "" {
			orders = append(orders, controller.ProductionOrder{BuildingID: id, UnitType: unit, Enabled: true})
		}
	}
	plan.ProductionOrders = &orders
	plan.RecruitmentOrders, plan.RecruitmentPriorities = nil, nil
	plan.SpendOrder = slices.DeleteFunc(slices.Clone(plan.SpendOrder), func(kind string) bool { return kind == "recruitment" })
	return plan
}
