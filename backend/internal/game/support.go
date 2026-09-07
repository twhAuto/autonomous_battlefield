// Resolves autonomous treatment and movement toward friendly soldiers.
package game

import (
	"math/bits"
	"sort"
)

func eligiblePatient(healer, patient Unit) bool {
	return patient.ID != healer.ID && patient.FactionID == healer.FactionID && patient.Health > 0 && patient.HealAmount == 0 && patient.Class != "siege" && patient.Class != "support"
}

func moreInjured(left, right Unit) bool {
	// Compare fractions using integer arithmetic, independent of rounding.
	lh, ll := bits.Mul64(uint64(left.Health), uint64(right.MaxHealth))
	rh, rl := bits.Mul64(uint64(right.Health), uint64(left.MaxHealth))
	return lh < rh || lh == rh && ll < rl
}

func (m *Match) runHealer(unit *Unit) []Event {
	var patient *Unit
	for i := range m.state.Units {
		candidate := &m.state.Units[i]
		if !eligiblePatient(*unit, *candidate) || candidate.Health >= candidate.MaxHealth || HexDistance(unit.Position, candidate.Position) > 1 {
			continue
		}
		if patient == nil || moreInjured(*candidate, *patient) || !moreInjured(*patient, *candidate) && candidate.ID < patient.ID {
			patient = candidate
		}
	}
	if patient != nil {
		amount := min(unit.HealAmount, patient.MaxHealth-patient.Health)
		patient.Health += amount
		unit.HealTargetID = patient.ID
		unit.HealTargetPosition = clonePosition(&patient.Position)
		return []Event{m.event("unit_healed", map[string]any{"factionId": unit.FactionID, "healerId": unit.ID, "targetId": patient.ID, "amount": amount, "remainingHealth": patient.Health})}
	}
	return m.followPatient(unit)
}

type patientRoute struct {
	patient Unit
	path    []Position
}

func (m *Match) followPatient(unit *Unit) []Event {
	routes := []patientRoute{}
	for _, patient := range m.state.Units {
		if !eligiblePatient(*unit, patient) {
			continue
		}
		goals := m.positionsInRange(patient.Position, 1, 1)
		// An idle escort prefers the castle side of its soldier.
		if patient.Health == patient.MaxHealth {
			if castle := m.castle(unit.FactionID); castle != nil {
				rear := map[Position]bool{}
				for p := range goals {
					if HexDistance(p, castle.Position) < HexDistance(patient.Position, castle.Position) {
						rear[p] = true
					}
				}
				if path, ok := m.routeTo(*unit, rear); ok {
					routes = append(routes, patientRoute{patient, path})
					continue
				}
			}
		}
		if path, ok := m.routeTo(*unit, goals); ok {
			routes = append(routes, patientRoute{patient, path})
		}
	}
	if len(routes) == 0 {
		return nil
	}
	sort.Slice(routes, func(i, j int) bool {
		l, r := routes[i], routes[j]
		li, ri := l.patient.Health < l.patient.MaxHealth, r.patient.Health < r.patient.MaxHealth
		if li != ri {
			return li
		}
		if !li && (l.patient.Type == "vanguard") != (r.patient.Type == "vanguard") {
			return l.patient.Type == "vanguard"
		}
		if len(l.path) != len(r.path) {
			return len(l.path) < len(r.path)
		}
		if moreInjured(l.patient, r.patient) {
			return true
		}
		if moreInjured(r.patient, l.patient) {
			return false
		}
		return l.patient.ID < r.patient.ID
	})
	return m.moveAlong(unit, routes[0].path)
}

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

func (m *Match) routeTo(unit Unit, goals map[Position]bool) ([]Position, bool) {
	if goals[unit.Position] {
		return nil, true
	}
	occupied := m.occupied()
	delete(occupied, unit.Position)
	path := shortestPath(m.scenario.Map, unit.Position, goals, occupied)
	return path, len(path) > 0
}

func (m *Match) moveAlong(unit *Unit, path []Position) []Event {
	steps := min(unit.Movement, len(path))
	if steps == 0 {
		return nil
	}
	from := unit.Position
	unit.Position = path[steps-1]
	unit.MovedFrom = &from
	return []Event{m.event("unit_moved", map[string]any{"factionId": unit.FactionID, "unitId": unit.ID, "from": from, "to": unit.Position})}
}
