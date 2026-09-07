// Finds alternative enemy approach positions when a unit's route to the opposing castle is
// blocked.

package game

// enemyApproachGoals provides reachable attack positions when the castle route
// is blocked. It uses the same authoritative enemy entities as targetInRange;
// faction observation visibility remains independent of engine movement.
func (m *Match) enemyApproachGoals(unit Unit) map[Position]bool {
	targets := []Position{}
	for _, enemy := range m.state.Units {
		if enemy.FactionID != unit.FactionID && enemy.Health > 0 {
			targets = append(targets, enemy.Position)
		}
	}
	for _, building := range m.state.Buildings {
		if building.FactionID != unit.FactionID && building.Health > 0 {
			targets = append(targets, building.Position)
		}
	}
	if len(targets) == 0 {
		return nil
	}
	return m.approachGoals(unit, targets)
}

func targetsAsGoals(targets []Position) map[Position]bool {
	goals := make(map[Position]bool, len(targets))
	for _, p := range targets {
		goals[p] = true
	}
	return goals
}

func (m *Match) approachGoals(unit Unit, targets []Position) map[Position]bool {
	targetGoals := targetsAsGoals(targets)
	goals := map[Position]bool{}
	for y := 0; y < m.scenario.Map.Height; y++ {
		for x := 0; x < m.scenario.Map.Width; x++ {
			p := Position{X: x, Y: y}
			distance := nearestDistance(p, targetGoals)
			if m.scenario.Map.Traversable(p) && distance >= unit.MinimumRange && distance <= unit.Range {
				goals[p] = true
			}
		}
	}
	return goals

}
