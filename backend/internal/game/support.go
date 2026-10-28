// Resolves autonomous healing and healer movement toward friendly soldiers.
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
		if !eligiblePatient(*unit, *candidate) || candidate.Health >= candidate.MaxHealth || HexDistance(unit.Position, candidate.Position) > max(1, unit.HealRange) {
			continue
		}
		if patient == nil || moreInjured(*candidate, *patient) || !moreInjured(*patient, *candidate) && candidate.ID < patient.ID {
			patient = candidate
		}
	}
	if patient != nil {
		amount := min(unit.HealAmount, patient.MaxHealth-patient.Health)
		patient.Health += amount
		m.factions[unit.FactionID].stats.unit(unit.Type).HealthRestored += amount
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
		if path, ok := m.patientPath(*unit, patient); ok {
			routes = append(routes, patientRoute{patient, path})
		}
	}
	if len(routes) == 0 {
		return nil
	}
	sort.Slice(routes, func(i, j int) bool {
		return patientRouteLess(routes[i], routes[j])
	})
	return m.moveAlong(unit, routes[0].path)
}

func (m *Match) patientPath(unit Unit, patient Unit) ([]Position, bool) {
	goals := m.positionsInRange(patient.Position, 1, max(1, unit.HealRange))
	// An idle escort prefers the castle side of its soldier.
	if patient.Health == patient.MaxHealth {
		if castle := m.castle(unit.FactionID); castle != nil {
			rear := patientRearGoals(goals, patient.Position, castle.Position)
			if path, ok := m.routeTo(unit, rear); ok {
				return path, true
			}
		}
	}
	return m.routeTo(unit, goals)
}

func patientRearGoals(goals map[Position]bool, patient, castle Position) map[Position]bool {
	rear := map[Position]bool{}
	for p := range goals {
		if HexDistance(p, castle) < HexDistance(patient, castle) {
			rear[p] = true
		}
	}
	return rear
}

func patientRouteLess(l, r patientRoute) bool {
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
}
