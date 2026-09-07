// Projects faction-visible battle state using allied vision while keeping hidden enemy
// entities out of the returned view.

package game

import (
	"fmt"
)

func (m *Match) View(factionID string) (View, error) {
	return ProjectView(m.scenario.Map, m.state, factionID)
}

// ProjectView applies v1 fog-of-war to a persisted state snapshot. Terrain is
// always returned; hostile non-castle entities are omitted outside vision.
func ProjectView(gameMap Map, source State, factionID string) (View, error) {
	if factionID == "" {
		positions := allPositions(gameMap)
		return View{Map: CloneMap(gameMap), State: CloneState(source), VisiblePositions: positions}, nil
	}
	if !factionParticipates(source, factionID) {
		return View{}, fmt.Errorf("faction %q does not participate", factionID)
	}
	visible := visiblePositionsForFaction(gameMap, source, factionID)
	state := visibleState(source, factionID, visible)
	return View{Map: CloneMap(gameMap), State: state, VisiblePositions: sortedPositions(visible)}, nil
}

func factionParticipates(state State, factionID string) bool {
	for _, faction := range state.Factions {
		if faction.FactionID == factionID {
			return true
		}
	}
	return false
}

func visiblePositionsForFaction(gameMap Map, state State, factionID string) map[Position]bool {
	visible := make(map[Position]bool)
	for _, unit := range state.Units {
		if unit.FactionID == factionID && unit.Health > 0 {
			addVision(visible, gameMap, unit.Position, unit.Vision)
		}
	}
	for _, building := range state.Buildings {
		if building.FactionID == factionID && building.Health > 0 {
			addVision(visible, gameMap, building.Position, building.Vision)
		}
	}
	return visible
}

func visibleState(source State, factionID string, visible map[Position]bool) State {
	state := CloneState(source)
	state.Units = state.Units[:0]
	for _, unit := range source.Units {
		if unit.FactionID == factionID || visible[unit.Position] {
			state.Units = append(state.Units, unit)
		}
	}
	state.Buildings = state.Buildings[:0]
	for _, building := range source.Buildings {
		if building.FactionID == factionID || building.Type == BuildingCastle || visible[building.Position] {
			state.Buildings = append(state.Buildings, building)
		}
	}
	return state
}

func sortedPositions(values map[Position]bool) []Position {
	positions := make([]Position, 0, len(values))
	for position := range values {
		positions = append(positions, position)
	}
	sortPositions(positions)
	return positions
}

func addVision(visible map[Position]bool, m Map, origin Position, radius int) {
	for y := 0; y < m.Height; y++ {
		for x := 0; x < m.Width; x++ {
			position := Position{X: x, Y: y}
			if HexDistance(origin, position) <= radius {
				visible[position] = true
			}
		}
	}
}

func allPositions(m Map) []Position {
	result := make([]Position, 0, m.Width*m.Height)
	for y := 0; y < m.Height; y++ {
		for x := 0; x < m.Width; x++ {
			result = append(result, Position{X: x, Y: y})
		}
	}
	return result
}
