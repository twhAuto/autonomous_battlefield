// Chooses scripted construction under gold mines: economy follows the gold and
// Outposts extend territory toward free mines.

package game

import (
	"sort"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/game/controller"
)

// goldMineConstructionOrders gives each wanted kind one separated site: economy takes
// the legal mine nearest the faction's castle and other kinds take the legal ground
// nearest the opponent. With no mine in reach, an Outpost moves toward the nearest
// unclaimed mine.
func (m *Match) goldMineConstructionOrders(slot int) []controller.ConstructionOrder {
	factionID := m.scenario.Factions[slot].ID
	own, enemy := m.scenario.Map.CastleSpawns[slot], m.scenario.Map.CastleSpawns[1-slot]
	mines := sitesByDistance(m.legalGoldMineSites(factionID), own)
	ground := sitesByDistance(m.legalConstructionSites(factionID), enemy)
	orders := []controller.ConstructionOrder{}
	for _, kind := range m.defaultConstructionKinds(factionID) {
		candidates := ground
		if kind == BuildingEconomy {
			candidates = mines
		}
		if site, ok := firstSeparatedSite(candidates, orders); ok {
			orders = append(orders, controller.ConstructionOrder{BuildingType: kind, Position: site})
		}
		if len(orders) == 4 {
			return orders
		}
	}
	if len(mines) == 0 {
		if order, ok := m.expansionOrder(factionID, ground, orders); ok {
			orders = append(orders, order)
		}
	}
	return orders
}

// expansionOrder places an Outpost on the ground site nearest the closest unclaimed
// mine outside friendly reach, only when that site gets strictly closer to it.
func (m *Match) expansionOrder(factionID string, ground []controller.Coordinate, orders []controller.ConstructionOrder) (controller.ConstructionOrder, bool) {
	outpost := m.structureOfKind(BuildingOutpost)
	castle := m.castle(factionID)
	if outpost == "" || castle == nil {
		return controller.ConstructionOrder{}, false
	}
	target, reach, ok := m.expansionTarget(factionID, castle.Position)
	if !ok {
		return controller.ConstructionOrder{}, false
	}
	for _, site := range sitesByDistance(ground, target) {
		position := Position{X: site.X, Y: site.Y}
		if HexDistance(position, target) >= reach {
			break
		}
		if constructionOrdersSeparated(site, orders) {
			return controller.ConstructionOrder{BuildingType: outpost, Position: site}, true
		}
	}
	return controller.ConstructionOrder{}, false
}

// expansionTarget is the unclaimed mine nearest the castle that no living
// friendly building reaches, with the distance from the nearest friendly building.
func (m *Match) expansionTarget(factionID string, castle Position) (Position, int, bool) {
	candidates := []Position{}
	for _, mine := range m.unclaimedGoldMines() {
		if !m.friendlyReach(factionID, mine) {
			candidates = append(candidates, mine)
		}
	}
	if len(candidates) == 0 {
		return Position{}, 0, false
	}
	sortByDistance(candidates, castle)
	target := candidates[0]
	reach := int(^uint(0) >> 1)
	for _, b := range m.state.Buildings {
		if b.FactionID == factionID && b.Health > 0 {
			reach = min(reach, HexDistance(b.Position, target))
		}
	}
	return target, reach, true
}

func (m *Match) friendlyReach(factionID string, site Position) bool {
	for _, b := range m.state.Buildings {
		if b.FactionID == factionID && b.Health > 0 && HexDistance(b.Position, site) <= effectiveBuildRange(b.BuildRange) {
			return true
		}
	}
	return false
}

func (m *Match) structureOfKind(kind string) string {
	for _, b := range m.scenario.Ruleset.Structures {
		if b.Kind == kind {
			return b.ID
		}
	}
	return ""
}

func firstSeparatedSite(sites []controller.Coordinate, orders []controller.ConstructionOrder) (controller.Coordinate, bool) {
	for _, site := range sites {
		if constructionOrdersSeparated(site, orders) {
			return site, true
		}
	}
	return controller.Coordinate{}, false
}

// sitesByDistance orders sites nearest anchor first; ties keep row order.
func sitesByDistance(sites []controller.Coordinate, anchor Position) []controller.Coordinate {
	sorted := append([]controller.Coordinate(nil), sites...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return HexDistance(Position{X: sorted[i].X, Y: sorted[i].Y}, anchor) < HexDistance(Position{X: sorted[j].X, Y: sorted[j].Y}, anchor)
	})
	return sorted
}

func sortByDistance(positions []Position, anchor Position) {
	sort.SliceStable(positions, func(i, j int) bool {
		return HexDistance(positions[i], anchor) < HexDistance(positions[j], anchor)
	})
}
