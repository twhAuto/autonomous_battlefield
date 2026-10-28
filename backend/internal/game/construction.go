package game

import "github.com/twh09/autonomous_battlefield/backend/internal/game/controller"

const DefaultBuildRange = 3
const GoldDepositIncomeBonus int64 = 5
const ForestDefenseBonus int64 = 3

func effectiveBuildRange(value int) int {
	if value > 0 {
		return value
	}
	return DefaultBuildRange
}

// ConstructionReason is shared by legal-site projection and execution. Dead
// structures do not block spacing or extend territory. Units block placement.
func ConstructionReason(gameMap Map, state State, factionID string, site Position) string {
	if !gameMap.Buildable(site) {
		return "invalid_terrain"
	}
	inRange, reason := constructionBuildingRange(state, factionID, site)
	if reason != "" {
		return reason
	}
	for _, u := range state.Units {
		if u.Health > 0 && u.Position == site {
			return "site_occupied"
		}
	}
	if !inRange {
		return "outside_build_range"
	}
	return ""
}

// Check every living building for spacing before reporting friendly influence.
func constructionBuildingRange(state State, factionID string, site Position) (bool, string) {
	inRange := false
	for _, b := range state.Buildings {
		if b.Health <= 0 {
			continue
		}
		distance := HexDistance(site, b.Position)
		if distance == 0 {
			return false, "site_occupied"
		}
		if distance <= 1 {
			return false, "building_too_close"
		}
		if b.FactionID == factionID && claimsSite(b, state.Tick, site) {
			inRange = true
		}
	}
	return inRange, ""
}

// legalConstructionSites is shared by decision snapshots and scripted defaults, so
// a full shared building limit offers no site to either. Under gold mines it lists
// sites for non-economy structures; legalGoldMineSites lists economy sites.
func (m *Match) legalConstructionSites(factionID string) []controller.Coordinate {
	return m.legalSitesFor(factionID, "")
}

func (m *Match) legalSitesFor(factionID, kind string) []controller.Coordinate {
	sites := []controller.Coordinate{}
	limit := m.scenario.Ruleset.Buildings.MaximumBuildingCount
	if len(ConstructibleBuildings(m.scenario.Ruleset)) == 0 || limit > 0 && m.LivingStructureCount(factionID) >= limit {
		return sites
	}
	for _, p := range allPositions(m.scenario.Map) {
		if ConstructionReason(m.scenario.Map, m.state, factionID, p) == "" && m.placementReason(factionID, kind, p) == "" {
			sites = append(sites, controller.Coordinate{X: p.X, Y: p.Y})
		}
	}
	return sites
}
