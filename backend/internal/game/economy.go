// Applies faction income, construction, and recruitment using production-window rules,
// gold reserves, and available spawn cells.

package game

import (
	"fmt"
	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
)

func (m *Match) runEconomy(productionWindow bool) []Event {
	events := make([]Event, 0, 8)
	for slot, setup := range m.scenario.Factions {
		events = append(events, m.runFactionEconomy(slot, setup.ID, productionWindow)...)
	}
	return events
}

func (m *Match) runFactionEconomy(slot int, factionID string, productionWindow bool) []Event {
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
	plan := m.factions[factionID].activePlan
	for _, kind := range plan.SpendOrder {
		switch kind {
		case "upgrades":
			events = append(events, m.runUpgrades(factionID, plan)...)
		case "construction":
			events = append(events, m.runConstruction(slot, factionID, plan)...)
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

func (m *Match) runConstruction(_ int, factionID string, plan controller.PlanProposal) []Event {
	events := make([]Event, 0, len(plan.ConstructionPriorities))
	for _, order := range constructionOrders(plan) {
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
	state := m.factionState(factionID)
	if m.economyCount(factionID) >= rules.MaximumEconomyCount {
		return Building{}, "maximum_reached"
	}
	if m.buildingAt(site) != nil {
		return Building{}, "site_occupied"
	}
	if state.Gold-rules.EconomyCost < reserve {
		return Building{}, "reserve_required"
	}
	state.Gold -= rules.EconomyCost
	state.GoldSpent += rules.EconomyCost
	state.BuildingsBuilt++
	building := Building{ID: fmt.Sprintf("%s:economy:%03d", factionID, state.BuildingsBuilt), FactionID: factionID, Type: BuildingEconomy, Position: site, Health: rules.EconomyHealth, MaxHealth: rules.EconomyHealth, Defense: rules.EconomyDefense, Vision: rules.EconomyVision, Cost: rules.EconomyCost, Income: rules.EconomyIncome + m.factions[factionID].economyBonus}
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
	orders := expandedRecruitmentOrders(plan.RecruitmentOrders)
	purchased := 0
	attempted := 0
	sourceIDs := expandedRecruitmentSources(plan.RecruitmentOrders)
	// Explicit orders and expanded rosters bound the loop; legacy priorities
	// continue until reserve or castle spawn constraints stop recruitment.
	for (purchased < 6 || len(m.scenario.Ruleset.Structures) == 0) && (plan.RecruitmentOrders == nil || attempted < len(orders)) {
		source := castle
		if plan.RecruitmentOrders != nil {
			source = m.recruitmentSource(factionID, sourceIDs[attempted])
			attempted++
		}
		priorities := plan.RecruitmentPriorities
		if plan.RecruitmentOrders != nil {
			priorities = orders[attempted-1 : attempted]
		}
		event, bought, stop := m.attemptRecruitment(factionID, source, definitions, priorities, plan.ReserveGold)
		events = append(events, event)
		if stop {
			break
		}
		if bought {
			purchased++
		}

	}
	return events
}

func (m *Match) recruitmentSpawn(castle Position) (Position, bool) {
	occupied := m.occupied()
	for _, candidate := range Neighbors(castle) {
		if m.scenario.Map.InBounds(candidate) && m.scenario.Map.Traversable(candidate) && !occupied[candidate] {
			return candidate, true
		}
	}
	return Position{}, false
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

func expandedRecruitmentOrders(orders *[]controller.RecruitmentOrder) []string {
	if orders == nil {
		return nil
	}
	result := []string{}
	for _, order := range *orders {
		for i := 0; i < order.Count; i++ {
			result = append(result, order.UnitType)
		}
	}
	return result
}

func expandedRecruitmentSources(orders *[]controller.RecruitmentOrder) []string {
	if orders == nil {
		return nil
	}
	result := []string{}
	for _, order := range *orders {
		for i := 0; i < order.Count; i++ {
			result = append(result, order.SourceBuildingID)
		}
	}
	return result
}

func (m *Match) purchaseRecruit(factionID string, selected UnitType, spawn Position, source *Building) Event {
	state := m.factionState(factionID)
	runtime := m.factions[factionID]
	state.Gold -= selected.Cost
	state.GoldSpent += selected.Cost
	state.UnitsRecruited++
	runtime.unitSequence++
	unit := runtime.newUnit(selected, spawn, m.state.Tick+1)
	m.state.Units = append(m.state.Units, unit)
	data := map[string]any{"factionId": factionID, "unitId": unit.ID, "unitType": unit.Type, "position": spawn, "readyAtTick": unit.ReadyAtTick, "cost": unit.Cost}
	if len(m.scenario.Ruleset.Structures) > 0 {
		data["sourceBuildingId"] = source.ID
	}
	return m.event("unit_recruited", data)
}

func (m *Match) attemptRecruitment(factionID string, source *Building, definitions map[string]UnitType, priorities []string, reserve int64) (Event, bool, bool) {
	skipped := func(reason string) Event {
		return m.event("production_skipped", map[string]any{"factionId": factionID, "kind": "recruitment", "reason": reason})
	}
	if source == nil || source.Health <= 0 || source.ReadyAtTick > m.state.Tick {
		return skipped("source_unavailable"), false, false
	}
	spawn, available := m.recruitmentSpawn(source.Position)
	if !available {
		return skipped("no_spawn"), false, source.Type != BuildingBarracks
	}
	selected := affordableUnit(definitions, priorities, m.factionState(factionID).Gold, reserve)
	if selected.ID == "" {
		return skipped("reserve_required"), false, true
	}
	return m.purchaseRecruit(factionID, selected, spawn, source), true, false
}
