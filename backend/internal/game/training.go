package game

import "sort"

const MaximumTrainingQueue = 12

// A building releases at most one troop each tick. Time does not advance for
// queued followers, nor for orders purchased during this same tick.
func (m *Match) runTraining() []Event {
	indices := make([]int, len(m.state.Buildings))
	for i := range indices {
		indices[i] = i
	}
	sort.Slice(indices, func(i, j int) bool { return m.state.Buildings[indices[i]].ID < m.state.Buildings[indices[j]].ID })
	definitions := UnitTypesByID(m.scenario.Ruleset)
	events := []Event{}
	for _, i := range indices {
		b := &m.state.Buildings[i]
		b.TrainingBlocked = false
		if b.Health <= 0 {
			b.TrainingQueue = nil
			continue
		}
		if len(b.TrainingQueue) == 0 || b.ReadyAtTick > m.state.Tick {
			continue
		}
		head := &b.TrainingQueue[0]
		if head.QueuedAtTick >= m.state.Tick {
			continue
		}
		if head.RemainingTicks > 0 {
			head.RemainingTicks--
		}
		if head.RemainingTicks > 0 {
			continue
		}
		spawn, available := m.recruitmentSpawn(b.Position)
		if !available {
			b.TrainingBlocked = true
			continue
		}
		runtime := m.factions[b.FactionID]
		runtime.unitSequence++
		unit := runtime.newUnit(definitions[head.UnitType], spawn, m.state.Tick+1)
		m.state.Units = append(m.state.Units, unit)
		if head.Opening {
			m.factionState(b.FactionID).UnitsStarted++
		} else {
			m.factionState(b.FactionID).UnitsRecruited++
		}
		b.TrainingQueue = b.TrainingQueue[1:]
		// Keep cost as informational unit value for existing event clients; only
		// unit_queued records a purchase. Deployment never charges gold.
		events = append(events, m.event("unit_recruited", map[string]any{"factionId": b.FactionID, "sourceBuildingId": b.ID, "unitId": unit.ID, "unitType": unit.Type, "position": spawn, "readyAtTick": unit.ReadyAtTick, "cost": unit.Cost}))
	}
	return events
}
