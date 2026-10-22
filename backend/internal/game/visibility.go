// Projects the open battlefield while retaining faction-private economy totals.
package game

import "fmt"

func (m *Match) View(factionID string) (View, error) {
	return ProjectView(m.scenario.Map, m.state, factionID)
}

// ProjectView exposes all terrain, entities and actions. The deprecated
// visiblePositions field contains the entire map for older API clients.
func ProjectView(gameMap Map, source State, factionID string) (View, error) {
	if factionID != "" && !factionParticipates(source, factionID) {
		return View{}, fmt.Errorf("faction %q does not participate", factionID)
	}
	state := CloneState(source)
	if factionID != "" {
		state.Factions = nil
		for _, faction := range source.Factions {
			if faction.FactionID == factionID {
				faction.OwnedUpgradeIDs = append([]string(nil), faction.OwnedUpgradeIDs...)
				state.Factions = append(state.Factions, faction)
			}
		}
	}
	return View{Map: CloneMap(gameMap), State: state, VisiblePositions: allPositions(gameMap)}, nil
}

func factionParticipates(state State, factionID string) bool {
	for _, faction := range state.Factions {
		if faction.FactionID == factionID {
			return true
		}
	}
	return false
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
