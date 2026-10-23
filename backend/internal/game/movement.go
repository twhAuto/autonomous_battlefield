// Moves units: goal sets, routing through friendly units around other occupants,
// stepping along a route without stacking, and alternative enemy approaches when a unit's route to the opposing castle is blocked.

package game

// moveUnit advances a unit toward attack range of the enemy castle, or toward any
// enemy it can reach when every castle approach is occupied. Free routes keep
// priority; passing through allies is the fallback for a jammed army.
func (m *Match) moveUnit(unit *Unit) []Event {
	castle := m.castle(m.opponentID(unit.FactionID))
	if castle == nil {
		return nil
	}
	castleGoals := m.positionsInRange(castle.Position, unit.MinimumRange, unit.Range)
	path, ok := m.route(*unit, castleGoals, false)
	// An occupied castle perimeter must not prevent approaching enemy blockers.
	var enemyGoals map[Position]bool
	if !ok {
		enemyGoals = m.enemyApproachGoals(*unit)
		path, ok = m.route(*unit, enemyGoals, false)
	}
	if !ok {
		path, ok = m.route(*unit, castleGoals, true)
	}
	if !ok {
		path, _ = m.route(*unit, enemyGoals, true)
	}
	return m.moveAlong(unit, path)
}

// positionsInRange returns the traversable hexes whose distance to target lies within
// [minimum, maximum].
func (m *Match) positionsInRange(target Position, minimum, maximum int) map[Position]bool {
	goals := map[Position]bool{}
	for y := 0; y < m.scenario.Map.Height; y++ {
		for x := 0; x < m.scenario.Map.Width; x++ {
			p := Position{X: x, Y: y}
			d := HexDistance(p, target)
			if d >= minimum && d <= maximum && m.scenario.Map.Traversable(p) {
				goals[p] = true
			}
		}
	}
	return goals
}

// routeTo finds the shortest route to an unoccupied goal, around every occupant
// when possible and otherwise through friendly units. It reports true with an
// empty route when the unit already stands on a goal.
func (m *Match) routeTo(unit Unit, goals map[Position]bool) ([]Position, bool) {
	if path, ok := m.route(unit, goals, false); ok {
		return path, true
	}
	return m.route(unit, goals, true)
}

// route never crosses enemies or buildings; throughAllies also permits friendly hexes.
func (m *Match) route(unit Unit, goals map[Position]bool, throughAllies bool) ([]Position, bool) {
	if goals[unit.Position] {
		return nil, true
	}
	occupied := m.occupied()
	delete(occupied, unit.Position)
	open := make(map[Position]bool, len(goals))
	for goal := range goals {
		if !occupied[goal] {
			open[goal] = true
		}
	}
	blocked := occupied
	if throughAllies {
		blocked = m.impassableFor(unit)
	}
	path := shortestPath(m.scenario.Map, unit.Position, open, blocked)
	return path, len(path) > 0
}

// impassableFor lists hexes a unit cannot cross: enemy units and living buildings.
func (m *Match) impassableFor(unit Unit) map[Position]bool {
	blocked := make(map[Position]bool)
	for _, other := range m.state.Units {
		if other.Health > 0 && other.FactionID != unit.FactionID {
			blocked[other.Position] = true
		}
	}
	for _, building := range m.state.Buildings {
		if building.Health > 0 {
			blocked[building.Position] = true
		}
	}
	return blocked
}

// moveAlong takes up to the unit's movement allowance of steps along path, ending
// on the furthest reachable hex that no other unit occupies; units never stack.
func (m *Match) moveAlong(unit *Unit, path []Position) []Event {
	steps := min(unit.Movement, len(path))
	occupied := m.occupied()
	for steps > 0 && occupied[path[steps-1]] {
		steps--
	}
	if steps == 0 {
		return nil
	}
	from := unit.Position
	unit.MovementPath = append([]Position{unit.Position}, path[:steps]...)
	unit.Position = path[steps-1]
	unit.MovedFrom = &from
	return []Event{m.event("unit_moved", map[string]any{"factionId": unit.FactionID, "unitId": unit.ID, "from": from, "to": unit.Position})}
}

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
