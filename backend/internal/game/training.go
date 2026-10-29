package game

const MaximumTrainingQueue = 12

// MaximumUnitCountLimit bounds authored per-faction unit caps.
const MaximumUnitCountLimit = 200

// UnitCount counts living units plus paid units still training, the total a
// faction's unit cap limits.
func (m *Match) UnitCount(factionID string) int {
	count := 0
	for _, unit := range m.state.Units {
		if unit.FactionID == factionID && unit.Health > 0 {
			count++
		}
	}
	for _, b := range m.state.Buildings {
		if b.FactionID == factionID && b.Health > 0 {
			count += len(b.TrainingQueue)
		}
	}
	return count
}

func (m *Match) atUnitLimit(factionID string) bool {
	limit := m.scenario.Ruleset.MaximumUnitCount
	return limit > 0 && m.UnitCount(factionID) >= limit
}

// A building releases at most one troop each tick. Time does not advance for
// queued followers, nor for orders purchased during this same tick.
func (m *Match) runTraining() []Event {
	indices := make([]int, len(m.state.Buildings))
	for i := range indices {
		indices[i] = i
	}
	m.sortBuildings(indices)
	definitions := UnitTypesByID(m.scenario.Ruleset)
	events := []Event{}
	for _, i := range indices {
		if event, deployed := m.trainBuilding(&m.state.Buildings[i], definitions); deployed {
			events = append(events, event)
		}
	}
	return events
}

// Advance a single source without advancing followers or charging for deployment.
func (m *Match) trainBuilding(b *Building, definitions map[string]UnitType) (Event, bool) {
	b.TrainingBlocked = false
	if b.Health <= 0 || len(m.trainingRoster(b.Type)) == 0 {
		b.TrainingQueue = nil
		return Event{}, false
	}
	if len(b.TrainingQueue) == 0 || b.ReadyAtTick > m.state.Tick {
		return Event{}, false
	}
	head := &b.TrainingQueue[0]
	if head.QueuedAtTick >= m.state.Tick {
		return Event{}, false
	}
	if head.RemainingTicks > 0 {
		head.RemainingTicks--
	}
	if head.RemainingTicks > 0 {
		return Event{}, false
	}
	spawn, available := m.recruitmentSpawn(b.Position)
	if !available {
		b.TrainingBlocked = true
		return Event{}, false
	}
	runtime := m.factions[b.FactionID]
	runtime.unitSequence++
	unit := runtime.newUnit(definitions[head.UnitType], spawn, m.state.Tick+1)
	m.state.Units = append(m.state.Units, unit)
	if head.Opening {
		m.factionState(b.FactionID).UnitsStarted++
		runtime.stats.unit(unit.Type).Started++
	} else {
		m.factionState(b.FactionID).UnitsRecruited++
		runtime.stats.unit(unit.Type).Recruited++
	}
	b.TrainingQueue = b.TrainingQueue[1:]
	// Keep cost as informational unit value for existing event clients; only
	// unit_queued records a purchase. Deployment never charges gold.
	return m.event("unit_recruited", map[string]any{"factionId": b.FactionID, "sourceBuildingId": b.ID, "unitId": unit.ID, "unitType": unit.Type, "position": spawn, "readyAtTick": unit.ReadyAtTick, "cost": unit.Cost}), true
}
