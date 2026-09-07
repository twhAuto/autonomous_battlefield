// Implements typed construction, recruitment sources, and static defenses.
package game

import (
	"fmt"
	"sort"

	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
)

// ConstructibleBuildings includes the legacy economy definition for a uniform API.
func ConstructibleBuildings(r Ruleset) []BuildingType {
	result := append([]BuildingType{}, r.Structures...)
	if r.Buildings.MaximumEconomyCount > 0 {
		b := r.Buildings
		result = append([]BuildingType{{ID: BuildingEconomy, Kind: BuildingEconomy, Cost: b.EconomyCost, Health: b.EconomyHealth, Defense: b.EconomyDefense, Vision: b.EconomyVision, Income: b.EconomyIncome}}, result...)
	}
	return result
}

func constructionOrders(plan controller.PlanProposal) []controller.ConstructionOrder {
	if plan.ConstructionOrders != nil {
		return *plan.ConstructionOrders
	}
	orders := make([]controller.ConstructionOrder, 0, len(plan.ConstructionPriorities))
	for _, p := range plan.ConstructionPriorities {
		orders = append(orders, controller.ConstructionOrder{BuildingType: BuildingEconomy, Position: p})
	}
	return orders
}

func (m *Match) buildStructureAt(factionID, kind string, site Position, reserve int64) (Building, string) {
	if m.scenario.Ruleset.Buildings.MaximumBuildingCount > 0 {
		count := m.livingStructureCount(factionID)
		if count >= m.scenario.Ruleset.Buildings.MaximumBuildingCount {
			return Building{}, "maximum_reached"
		}
	}
	if kind == BuildingEconomy {
		return m.buildEconomyBuildingAt(factionID, site, reserve)
	}
	var definition *BuildingType
	for i := range m.scenario.Ruleset.Structures {
		if m.scenario.Ruleset.Structures[i].ID == kind {
			definition = &m.scenario.Ruleset.Structures[i]
			break
		}
	}
	if definition == nil {
		return Building{}, "unsupported_building"
	}
	if m.buildingAt(site) != nil {
		return Building{}, "site_occupied"
	}
	state := m.factionState(factionID)
	if state.Gold-definition.Cost < reserve {
		return Building{}, "reserve_required"
	}
	state.Gold -= definition.Cost
	state.GoldSpent += definition.Cost
	state.BuildingsBuilt++
	b := Building{ID: fmt.Sprintf("%s:%s:%03d", factionID, kind, state.BuildingsBuilt), FactionID: factionID, Type: definition.Kind, Position: site, Health: definition.Health, MaxHealth: definition.Health, Defense: definition.Defense, Vision: definition.Vision, Cost: definition.Cost, Attack: definition.Attack, Range: definition.Range, ReadyAtTick: m.state.Tick + 1}
	m.state.Buildings = append(m.state.Buildings, b)
	return b, ""
}

func (m *Match) recruitmentSource(factionID, id string) *Building {
	if id == "" {
		return m.castle(factionID)
	}
	for i := range m.state.Buildings {
		b := &m.state.Buildings[i]
		if b.ID == id && b.FactionID == factionID && b.Health > 0 && (b.Type == BuildingCastle || b.Type == BuildingBarracks) {
			return b
		}
	}
	return nil
}

func (m *Match) recruitmentSources(factionID string) []string {
	ids := []string{}
	for _, b := range m.state.Buildings {
		if b.FactionID == factionID && b.Health > 0 && (b.Type == BuildingCastle || b.Type == BuildingBarracks) {
			ids = append(ids, b.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

func (m *Match) runTowers(tactics map[string]string) []Event {
	indices := []int{}
	for i, b := range m.state.Buildings {
		if b.Health > 0 && b.Attack > 0 && b.ReadyAtTick <= m.state.Tick {
			indices = append(indices, i)
		}
	}
	sort.Slice(indices, func(i, j int) bool { return m.state.Buildings[indices[i]].ID < m.state.Buildings[indices[j]].ID })
	events := []Event{}
	for _, index := range indices {
		b := &m.state.Buildings[index]
		attacker := Unit{ID: b.ID, FactionID: b.FactionID, Position: b.Position, Attack: b.Attack, Range: b.Range}
		target := m.towerTarget(b, attacker)
		if target == nil {
			continue
		}
		attackEvents, _ := m.attackTarget(&attacker, target, tactics)
		attackEvents[0].Type = "building_attacked"
		b.AttackTargetID, b.AttackTargetPosition = attacker.AttackTargetID, attacker.AttackTargetPosition
		events = append(events, attackEvents...)
	}
	return events
}

// Default scripted plans use rear income and exposed defense/reinforcement sites.
// Explicit model orders (including an empty list) always take precedence.
func (m *Match) defaultConstructionOrders(slot int) []controller.ConstructionOrder {
	sites := m.scenario.Map.EconomySites[slot]
	orders := []controller.ConstructionOrder{}
	add := func(kind string, index int) {
		p := sites[index]
		orders = append(orders, controller.ConstructionOrder{BuildingType: kind, Position: controller.Coordinate{X: p.X, Y: p.Y}})
	}
	add(BuildingEconomy, 0)
	available := map[string]bool{}
	for _, b := range m.scenario.Ruleset.Structures {
		available[b.Kind] = true
	}
	if len(sites) >= 4 && available[BuildingTower] {
		add(BuildingTower, len(sites)-2)
	}
	if len(sites) >= 4 && available[BuildingBarracks] {
		add(BuildingBarracks, len(sites)-1)
	}
	add(BuildingEconomy, 1)
	return orders
}

func (m *Match) assignForwardRecruitment(factionID string, plan *controller.PlanProposal) {
	if plan.RecruitmentOrders == nil {
		return
	}
	castle := m.castle(m.opponentID(factionID))
	if castle == nil {
		return
	}
	var source *Building
	for i := range m.state.Buildings {
		b := &m.state.Buildings[i]
		if b.Type != BuildingBarracks || b.Health <= 0 || b.FactionID != factionID || b.ReadyAtTick > m.state.Tick {
			continue
		}
		if source == nil || HexDistance(b.Position, castle.Position) < HexDistance(source.Position, castle.Position) || HexDistance(b.Position, castle.Position) == HexDistance(source.Position, castle.Position) && b.ID < source.ID {
			source = b
		}
	}
	if source == nil {
		return
	}
	for i := range *plan.RecruitmentOrders {
		if (*plan.RecruitmentOrders)[i].SourceBuildingID == "" {
			(*plan.RecruitmentOrders)[i].SourceBuildingID = source.ID
		}
	}
}

func (m *Match) livingStructureCount(factionID string) int {
	count := 0
	for _, b := range m.state.Buildings {
		if b.FactionID == factionID && b.Health > 0 && b.Type != BuildingCastle {
			count++
		}
	}
	return count
}

func (m *Match) towerTarget(b *Building, attacker Unit) *attackTarget {
	var target *attackTarget
	for i := range m.state.Units {
		u := &m.state.Units[i]
		if u.Health <= 0 || u.FactionID == b.FactionID || HexDistance(u.Position, b.Position) > b.Range {
			continue
		}
		candidate := &attackTarget{id: u.ID, kind: "unit", factionID: u.FactionID, position: u.Position, defense: u.Defense, cost: u.Cost, unit: u}
		if target == nil || attackTargetLess(attacker, candidate, target) {
			target = candidate
		}
	}
	return target
}
