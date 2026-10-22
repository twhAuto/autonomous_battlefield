// Applies faction income, construction, and recruitment using production-window rules,
// gold reserves, and available spawn cells.

package game

import (
	"fmt"

	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
)

func (m *Match) runEconomy(productionWindow bool) []Event {
	events := make([]Event, 0, 8)
	for _, setup := range m.scenario.Factions {
		events = append(events, m.runFactionEconomy(setup.ID, productionWindow)...)
	}
	return events
}

func (m *Match) runFactionEconomy(factionID string, productionWindow bool) []Event {
	castle := m.castle(factionID)
	if castle == nil || castle.Health <= 0 {
		return nil
	}
	state := m.factionState(factionID)
	income := m.incomeFor(factionID, castle.Income)
	state.Gold += income
	state.GoldEarned += income
	events := []Event{m.event("income_collected", map[string]any{"factionId": factionID, "amount": income, "balance": state.Gold})}
	if !productionWindow {
		return events
	}
	return append(events, m.spend(factionID, m.factions[factionID].activePlan, true)...)
}

// spend buys in the plan's spend order. The opening allows no construction.
func (m *Match) spend(factionID string, plan controller.PlanProposal, construction bool) []Event {
	events := []Event{}
	for _, kind := range plan.SpendOrder {
		switch kind {
		case "upgrades":
			events = append(events, m.runUpgrades(factionID, plan)...)
		case "construction":
			if construction {
				events = append(events, m.runConstruction(factionID, plan)...)
			}
		case "recruitment":
			events = append(events, m.runRecruitment(factionID, plan)...)
		}
	}
	return events
}

func (m *Match) incomeFor(factionID string, castleIncome int64) int64 {
	income := castleIncome
	for index := range m.state.Buildings {
		building := &m.state.Buildings[index]
		if building.FactionID == factionID && building.Type == BuildingEconomy && building.Health > 0 {
			income += building.Income
		}
	}
	return income
}

func (m *Match) runConstruction(factionID string, plan controller.PlanProposal) []Event {
	orders := constructionOrders(plan)
	events := make([]Event, 0, len(orders))
	for _, order := range orders {
		coordinate := order.Position
		site := Position{X: coordinate.X, Y: coordinate.Y}
		building, reason := m.buildStructureAt(factionID, order.BuildingType, site, plan.ReserveGold)
		if reason != "" {
			events = append(events, m.event("production_skipped", map[string]any{"factionId": factionID, "kind": "construction", "position": site, "reason": reason}))
			continue
		}
		events = append(events, m.event("building_constructed", map[string]any{"factionId": factionID, "buildingId": building.ID, "buildingType": building.Type, "position": building.Position, "cost": building.Cost}))
	}
	return events
}

func (m *Match) buildEconomyBuildingAt(factionID string, site Position, reserve int64) (Building, string) {
	rules := m.scenario.Ruleset.Buildings
	if m.economyCount(factionID) >= rules.MaximumEconomyCount {
		return Building{}, "maximum_reached"
	}
	state, reason := m.payForBuilding(factionID, site, rules.EconomyCost, reserve)
	if reason != "" {
		return Building{}, reason
	}
	building := Building{BuildRange: effectiveBuildRange(rules.EconomyBuildRange), ID: fmt.Sprintf("%s:economy:%03d", factionID, state.BuildingsBuilt), FactionID: factionID, Type: BuildingEconomy, Position: site, Health: rules.EconomyHealth, MaxHealth: rules.EconomyHealth, Defense: rules.EconomyDefense, Vision: rules.EconomyVision, Cost: rules.EconomyCost, Income: rules.EconomyIncome + m.factions[factionID].economyBonus}
	if m.scenario.Map.TerrainAt(site) == TerrainGold {
		building.Income += GoldDepositIncomeBonus
	}
	m.state.Buildings = append(m.state.Buildings, building)
	return building, ""
}

func (m *Match) runRecruitment(factionID string, plan controller.PlanProposal) []Event {
	castle := m.castle(factionID)
	if castle == nil || castle.Health <= 0 {
		return nil
	}
	definitions := UnitTypesByID(m.scenario.Ruleset)
	events := make([]Event, 0, 6)
	explicit := plan.RecruitmentOrders != nil
	queue := expandRecruitmentOrders(plan.RecruitmentOrders)
	purchased := 0
	// Explicit orders and expanded rosters bound the loop; legacy priorities
	// stop at six purchases, the reserve, or a full/unavailable source.
	for attempt := 0; purchased < 6 && (!explicit || attempt < len(queue)); attempt++ {
		source, priorities := castle, plan.RecruitmentPriorities
		if explicit {
			source = m.recruitmentSource(factionID, queue[attempt].SourceBuildingID)
			priorities = []string{queue[attempt].UnitType}
		}
		event, bought, stop := m.attemptRecruitment(factionID, source, definitions, priorities, plan.ReserveGold)
		events = append(events, event)
		if stop || !explicit && !bought {
			break
		}
		if bought {
			purchased++
		}
	}
	return events
}

func (m *Match) recruitmentSpawn(source Position) (Position, bool) {
	if spawns := freeNeighbors(m.scenario.Map, source, m.occupied()); len(spawns) > 0 {
		return spawns[0], true
	}
	return Position{}, false
}

// freeNeighbors lists unoccupied traversable hexes around center in Neighbors order.
func freeNeighbors(gameMap Map, center Position, occupied map[Position]bool) []Position {
	free := make([]Position, 0, 6)
	for _, candidate := range Neighbors(center) {
		if gameMap.InBounds(candidate) && gameMap.Traversable(candidate) && !occupied[candidate] {
			free = append(free, candidate)
		}
	}
	return free
}

// payForBuilding checks the site and reserve, then charges the faction.
func (m *Match) payForBuilding(factionID string, site Position, cost, reserve int64) (*FactionState, string) {
	if reason := ConstructionReason(m.scenario.Map, m.state, factionID, site); reason != "" {
		return nil, reason
	}
	state := m.factionState(factionID)
	if state.Gold-cost < reserve {
		return nil, "reserve_required"
	}
	state.Gold -= cost
	state.GoldSpent += cost
	state.BuildingsBuilt++
	return state, ""
}

func affordableUnit(definitions map[string]UnitType, priorities []string, gold, reserve int64) UnitType {
	for _, unitType := range priorities {
		definition := definitions[unitType]
		if gold-definition.Cost >= reserve {
			return definition
		}
	}
	return UnitType{}
}

func (m *Match) economyCount(id string) int {
	count := 0
	for _, building := range m.state.Buildings {
		if building.FactionID == id && building.Type == BuildingEconomy && building.Health > 0 {
			count++
		}
	}
	return count
}

// expandRecruitmentOrders turns each order into Count single-unit orders.
func expandRecruitmentOrders(orders *[]controller.RecruitmentOrder) []controller.RecruitmentOrder {
	if orders == nil {
		return nil
	}
	result := []controller.RecruitmentOrder{}
	for _, order := range *orders {
		for i := 0; i < order.Count; i++ {
			result = append(result, controller.RecruitmentOrder{SourceBuildingID: order.SourceBuildingID, UnitType: order.UnitType, Count: 1})
		}
	}
	return result
}

func (m *Match) attemptRecruitment(factionID string, source *Building, definitions map[string]UnitType, priorities []string, reserve int64) (Event, bool, bool) {
	skipped := func(reason string) Event {
		return m.event("production_skipped", map[string]any{"factionId": factionID, "kind": "recruitment", "reason": reason})
	}
	if source == nil || source.Health <= 0 || source.ReadyAtTick > m.state.Tick {
		return skipped("source_unavailable"), false, false
	}
	if len(source.TrainingQueue) >= MaximumTrainingQueue {
		return skipped("queue_full"), false, false
	}
	selected := affordableUnit(definitions, priorities, m.factionState(factionID).Gold, reserve)
	if selected.ID == "" {
		return skipped("reserve_required"), false, true
	}
	state := m.factionState(factionID)
	state.Gold -= selected.Cost
	state.GoldSpent += selected.Cost
	ticks := max(uint64(1), selected.TrainingTicks)
	source.TrainingQueue = append(source.TrainingQueue, TrainingOrder{UnitType: selected.ID, RemainingTicks: ticks, TrainingTicks: ticks, QueuedAtTick: m.state.Tick, Opening: m.OpeningDue()})
	return m.event("unit_queued", map[string]any{"factionId": factionID, "sourceBuildingId": source.ID, "unitType": selected.ID, "trainingTicks": ticks, "cost": selected.Cost}), true, false
}
