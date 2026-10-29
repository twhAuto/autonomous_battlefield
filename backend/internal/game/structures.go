// Implements typed construction, recruitment sources, and static defenses.
package game

import (
	"fmt"
	"sort"

	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
)

// ConstructibleBuildings includes the legacy economy definition for a uniform API.
func ConstructibleBuildings(r Ruleset) []BuildingType {
	result := CloneBuildingTypes(r.Structures)
	if EconomyAvailable(r.Buildings) {
		b := r.Buildings
		result = append([]BuildingType{{BuildRange: effectiveBuildRange(b.EconomyBuildRange), ID: BuildingEconomy, Kind: BuildingEconomy, Cost: b.EconomyCost, Health: b.EconomyHealth, Defense: b.EconomyDefense, Vision: b.EconomyVision, Income: b.EconomyIncome}}, result...)
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
		count := m.LivingStructureCount(factionID)
		if count >= m.scenario.Ruleset.Buildings.MaximumBuildingCount {
			return Building{}, "maximum_reached"
		}
	}
	if reason := m.placementReason(factionID, kind, site); reason != "" {
		return Building{}, reason
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
	state, reason := m.payForBuilding(factionID, definition.Kind, site, definition.Cost, reserve)
	if reason != "" {
		return Building{}, reason
	}
	b := Building{Trains: append([]string(nil), definition.Trains...), BuildRange: effectiveBuildRange(definition.BuildRange), ID: fmt.Sprintf("%s:%s:%03d", factionID, kind, state.BuildingsBuilt), FactionID: factionID, Type: definition.Kind, Position: site, Health: definition.Health, MaxHealth: definition.Health, Defense: definition.Defense, Vision: definition.Vision, Cost: definition.Cost, Attack: definition.Attack, Range: definition.Range, ReadyAtTick: m.state.Tick + 1}
	m.initializeProduction(&b)
	m.state.Buildings = append(m.state.Buildings, b)
	return b, ""
}

func (m *Match) recruitmentSource(factionID, id string) *Building {
	for i := range m.state.Buildings {
		b := &m.state.Buildings[i]
		if b.ID == id && b.FactionID == factionID && b.Health > 0 && len(m.trainingRoster(b.Type)) > 0 {
			return b
		}
	}
	return nil
}

func (m *Match) recruitmentSources(factionID string) []string {
	ids := []string{}
	for _, b := range m.state.Buildings {
		if b.FactionID == factionID && b.Health > 0 && b.ReadyAtTick <= m.state.Tick && len(m.trainingRoster(b.Type)) > 0 {
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
	m.sortBuildings(indices)
	events := []Event{}
	for _, index := range indices {
		b := &m.state.Buildings[index]
		attacker := Unit{ID: b.ID, FactionID: b.FactionID, Position: b.Position, Attack: b.Attack, Range: b.Range}
		target := m.towerTarget(b, attacker)
		if target == nil {
			continue
		}
		attackEvents, _ := m.strike(&attacker, b, target, tactics)
		attackEvents[0].Type = "building_attacked"
		b.AttackTargetID, b.AttackTargetPosition = attacker.AttackTargetID, attacker.AttackTargetPosition
		b.AttackOutcome = attacker.AttackOutcome
		events = append(events, attackEvents...)
	}
	return events
}

// Default plans choose currently legal, separated sites, favoring deposits.
func (m *Match) defaultConstructionOrders(slot int) []controller.ConstructionOrder {
	if m.scenario.Ruleset.Buildings.GoldMinesEnabled() {
		return m.goldMineConstructionOrders(slot)
	}
	factionID := m.scenario.Factions[slot].ID
	sites := m.legalConstructionSites(factionID)
	enemy := m.scenario.Map.CastleSpawns[1-slot]
	sort.SliceStable(sites, func(i, j int) bool {
		a, b := Position{X: sites[i].X, Y: sites[i].Y}, Position{X: sites[j].X, Y: sites[j].Y}
		ga, gb := m.scenario.Map.TerrainAt(a) == TerrainGold, m.scenario.Map.TerrainAt(b) == TerrainGold
		if ga != gb {
			return ga
		}
		return HexDistance(a, enemy) < HexDistance(b, enemy)
	})
	kinds := m.defaultConstructionKinds(factionID)
	orders := []controller.ConstructionOrder{}
	for _, site := range sites {
		if len(kinds) == 0 || m.specializedTraining() && len(orders) >= len(kinds) {
			break
		}
		if !constructionOrdersSeparated(site, orders) {
			continue
		}
		orders = append(orders, controller.ConstructionOrder{BuildingType: kinds[len(orders)%len(kinds)], Position: site})
		if len(orders) == 4 {
			break
		}
	}
	return orders
}

func constructionOrdersSeparated(site controller.Coordinate, orders []controller.ConstructionOrder) bool {
	for _, order := range orders {
		if HexDistance(Position{X: site.X, Y: site.Y}, Position{X: order.Position.X, Y: order.Position.Y}) <= 1 {
			return false
		}
	}
	return true
}

func (m *Match) assignForwardRecruitment(factionID string, plan *controller.PlanProposal) {
	if plan.RecruitmentOrders == nil || m.specializedTraining() {
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

// MaximumBuildingCountLimit bounds authored shared structure limits so every owned
// source, castle included, fits in one plan's production orders. Rulesets without a
// limit can own more sources; omitted sources keep their settings across windows.
const MaximumBuildingCountLimit = controller.MaximumProductionOrders - 1

// LivingStructureCount counts the non-castle buildings that share maximumBuildingCount.
func (m *Match) LivingStructureCount(factionID string) int {
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

// Fill each production capability before spending on duplicate training sites.
func (m *Match) missingConstructionKinds(factionID string) []string {
	owned := map[string]int{}
	for _, b := range m.state.Buildings {
		if b.FactionID == factionID && b.Health > 0 {
			owned[b.Type]++
		}
	}
	kinds := []string{}
	if rules := m.scenario.Ruleset.Buildings; EconomyAvailable(rules) && !economyLimitReached(rules, owned[BuildingEconomy]) {
		kinds = append(kinds, BuildingEconomy)
	}
	for _, b := range m.scenario.Ruleset.Structures {
		if owned[b.Kind] == 0 && (len(b.Trains) > 0 || b.Kind == BuildingTower) {
			kinds = append(kinds, b.ID)
		}
	}
	return kinds
}

func (m *Match) defaultConstructionKinds(factionID string) []string {
	if m.specializedTraining() {
		return m.missingConstructionKinds(factionID)
	}
	kinds := []string{BuildingEconomy}
	for _, b := range m.scenario.Ruleset.Structures {
		if b.Kind == BuildingBarracks || b.Kind == BuildingTower {
			kinds = append(kinds, b.ID)
		}
	}
	return kinds
}
