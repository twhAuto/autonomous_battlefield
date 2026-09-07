// Implements siege approach priorities and escape from minimum-range threats.
package game

import "sort"

func siegePriority(kind string) int {
	switch kind {
	case BuildingTower:
		return 0
	case BuildingCastle:
		return 2
	case "unit":
		return 3
	default:
		return 1
	}
}

func (m *Match) moveSiege(unit *Unit) []Event {
	targets := []Building{}
	for _, b := range m.state.Buildings {
		if b.Health > 0 && b.FactionID != unit.FactionID {
			targets = append(targets, b)
		}
	}
	sort.Slice(targets, func(i, j int) bool {
		l, r := targets[i], targets[j]
		if siegePriority(l.Type) != siegePriority(r.Type) {
			return siegePriority(l.Type) < siegePriority(r.Type)
		}
		ld, rd := HexDistance(unit.Position, l.Position), HexDistance(unit.Position, r.Position)
		if ld != rd {
			return ld < rd
		}
		return l.ID < r.ID
	})
	for _, b := range targets {
		if path, ok := m.routeTo(*unit, m.positionsInRange(b.Position, max(1, unit.MinimumRange), unit.Range)); ok {
			return m.moveAlong(unit, path)
		}
	}
	// Approach blockers if all structures are screened by occupied cells.
	path, _ := m.routeTo(*unit, m.enemyApproachGoals(*unit))
	return m.moveAlong(unit, path)
}

func (m *Match) retreatFromCloseEnemies(unit *Unit) ([]Event, bool) {
	if unit.MinimumRange <= 1 {
		return nil, false
	}
	threats := []Position{}
	for _, enemy := range m.state.Units {
		if enemy.Health > 0 && enemy.FactionID != unit.FactionID {
			threats = append(threats, enemy.Position)
		}
	}
	if len(threats) == 0 {
		return nil, false
	}
	goals := targetsAsGoals(threats)
	currentDistance := nearestDistance(unit.Position, goals)
	if currentDistance >= unit.MinimumRange {
		return nil, false
	}
	// Only choose destinations reachable in this action, without crossing occupants.
	occupied := m.occupied()
	paths := map[Position][]Position{unit.Position: nil}
	queue := []Position{unit.Position}
	best := unit.Position
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		if nearestDistance(p, goals) > nearestDistance(best, goals) {
			best = p
		}
		if len(paths[p]) >= unit.Movement {
			continue
		}
		for _, next := range Neighbors(p) {
			if _, seen := paths[next]; seen || occupied[next] || !m.scenario.Map.Traversable(next) {
				continue
			}
			paths[next] = append(append([]Position{}, paths[p]...), next)
			queue = append(queue, next)
		}
	}
	if best == unit.Position {
		return nil, false
	}
	return m.moveAlong(unit, paths[best]), true
}
