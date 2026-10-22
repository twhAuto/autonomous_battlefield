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
	inRange := false
	for _, b := range state.Buildings {
		if b.Health <= 0 {
			continue
		}
		distance := HexDistance(site, b.Position)
		if distance == 0 {
			return "site_occupied"
		}
		if distance <= 1 {
			return "building_too_close"
		}
		if b.FactionID == factionID && b.ReadyAtTick <= state.Tick && distance <= effectiveBuildRange(b.BuildRange) {
			inRange = true
		}
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

func (m *Match) legalConstructionSites(factionID string) []controller.Coordinate {
	sites := []controller.Coordinate{}
	if len(ConstructibleBuildings(m.scenario.Ruleset)) == 0 {
		return sites
	}
	for _, p := range allPositions(m.scenario.Map) {
		if ConstructionReason(m.scenario.Map, m.state, factionID, p) == "" {
			sites = append(sites, controller.Coordinate{X: p.X, Y: p.Y})
		}
	}
	return sites
}
