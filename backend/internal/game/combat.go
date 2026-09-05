package game

import (
	"sort"
)

func (m *Match) runUnits(tactics map[string]string) ([]Event, *string) {
	defer m.removeDestroyedUnits()
	indices := m.unitTurnOrder()
	events := make([]Event, 0, len(indices)*2)
	for _, index := range indices {
		unit := &m.state.Units[index]
		if unit.Health <= 0 || unit.ReadyAtTick > m.state.Tick {
			continue
		}
		unitEvents, winner := m.runUnit(unit, tactics)
		events = append(events, unitEvents...)
		if winner != nil {
			return events, winner
		}
	}
	return events, nil
}

func (m *Match) unitTurnOrder() []int {
	indices := make([]int, 0, len(m.state.Units))
	for index := range m.state.Units {
		indices = append(indices, index)
	}
	sort.Slice(indices, func(i, j int) bool {
		left, right := m.state.Units[indices[i]], m.state.Units[indices[j]]
		if left.Speed != right.Speed {
			return left.Speed > right.Speed
		}
		return left.ID < right.ID
	})
	return indices
}

func (m *Match) runUnit(unit *Unit, tactics map[string]string) ([]Event, *string) {
	target := m.targetInRange(*unit)
	if target == nil {
		return m.moveUnit(unit), nil
	}
	return m.attackTarget(unit, target, tactics)
}

func (m *Match) moveUnit(unit *Unit) []Event {
	from := unit.Position
	m.moveTowardCastle(unit)
	if unit.Position == from {
		return nil
	}
	unit.MovedFrom = &from
	return []Event{m.event("unit_moved", map[string]any{"factionId": unit.FactionID, "unitId": unit.ID, "from": from, "to": unit.Position})}
}

func (m *Match) attackTarget(unit *Unit, target *attackTarget, tactics map[string]string) ([]Event, *string) {
	damage := m.damage(*unit, target.defense, tactics[unit.FactionID], tactics[target.factionID])
	targetPosition := target.position
	unit.AttackTargetID = target.id
	unit.AttackTargetPosition = &targetPosition
	actual := min(damage, target.health())
	target.applyDamage(actual)
	m.factions[unit.FactionID].damageDealt += actual
	m.factions[target.factionID].damageTaken += actual
	events := []Event{m.event("unit_attacked", map[string]any{"attackerId": unit.ID, "targetId": target.id, "targetType": target.kind, "damage": actual, "remainingHealth": target.health()})}
	if target.health() > 0 {
		return events, nil
	}
	if target.kind == BuildingCastle {
		events = append(events, m.event("castle_destroyed", map[string]any{"castleId": target.id, "factionId": target.factionID, "destroyedByFactionId": unit.FactionID}))
		winner := unit.FactionID
		return events, &winner
	}
	events = append(events, m.destroyTargetEvent(unit.FactionID, target))
	if lootEvent, ok := m.collectLoot(unit.FactionID, target); ok {
		events = append(events, lootEvent)
	}
	return events, nil
}

func (m *Match) destroyTargetEvent(attackerFactionID string, target *attackTarget) Event {
	if target.kind == "unit" {
		m.factionState(target.factionID).UnitsLost++
		return m.event("unit_destroyed", map[string]any{"unitId": target.id, "factionId": target.factionID})
	}
	m.factionState(attackerFactionID).BuildingsDestroyed++
	return m.event("building_destroyed", map[string]any{"buildingId": target.id, "factionId": target.factionID, "buildingType": target.kind})
}

func (m *Match) collectLoot(factionID string, target *attackTarget) (Event, bool) {
	loot := target.cost * m.scenario.Ruleset.Combat.LootPercent / 100
	if loot <= 0 {
		return Event{}, false
	}
	owner := m.factionState(factionID)
	owner.Gold += loot
	owner.GoldEarned += loot
	owner.GoldLooted += loot
	return m.event("loot_collected", map[string]any{"factionId": factionID, "sourceId": target.id, "amount": loot, "balance": owner.Gold}), true
}

func (m *Match) clearUnitActions() {
	for index := range m.state.Units {
		m.state.Units[index].MovedFrom = nil
		m.state.Units[index].AttackTargetID = ""
		m.state.Units[index].AttackTargetPosition = nil
	}
}

func (m *Match) removeDestroyedUnits() {
	survivors := make([]Unit, 0, len(m.state.Units))
	for _, unit := range m.state.Units {
		if unit.Health > 0 {
			survivors = append(survivors, unit)
		}
	}
	m.state.Units = survivors
}

type attackTarget struct {
	id, kind, factionID string
	position            Position
	defense, cost       int64
	unit                *Unit
	building            *Building
}

func (t *attackTarget) health() int64 {
	if t.unit != nil {
		return t.unit.Health
	}
	return t.building.Health
}

func (t *attackTarget) applyDamage(value int64) {
	if t.unit != nil {
		t.unit.Health -= value
	} else {
		t.building.Health -= value
	}
}

func (m *Match) targetInRange(attacker Unit) *attackTarget {
	candidates := make([]*attackTarget, 0)
	for index := range m.state.Units {
		unit := &m.state.Units[index]
		if unit.FactionID != attacker.FactionID && unit.Health > 0 && HexDistance(attacker.Position, unit.Position) <= attacker.Range {
			candidates = append(candidates, &attackTarget{id: unit.ID, kind: "unit", factionID: unit.FactionID, position: unit.Position, defense: unit.Defense, cost: unit.Cost, unit: unit})
		}
	}
	for index := range m.state.Buildings {
		building := &m.state.Buildings[index]
		if building.FactionID != attacker.FactionID && building.Health > 0 && HexDistance(attacker.Position, building.Position) <= attacker.Range {
			candidates = append(candidates, &attackTarget{id: building.ID, kind: building.Type, factionID: building.FactionID, position: building.Position, defense: building.Defense, cost: building.Cost, building: building})
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		return attackTargetLess(attacker, candidates[i], candidates[j])
	})
	return candidates[0]
}

func attackTargetLess(attacker Unit, left, right *attackTarget) bool {
	if attackTargetPriority(left.kind) != attackTargetPriority(right.kind) {
		return attackTargetPriority(left.kind) < attackTargetPriority(right.kind)
	}
	leftDistance, rightDistance := HexDistance(attacker.Position, left.position), HexDistance(attacker.Position, right.position)
	if leftDistance != rightDistance {
		return leftDistance < rightDistance
	}
	if left.health() != right.health() {
		return left.health() < right.health()
	}
	return left.id < right.id
}

func attackTargetPriority(kind string) int {
	if kind == "unit" {
		return 0
	}
	if kind == BuildingEconomy {
		return 1
	}
	return 2
}

func (m *Match) moveTowardCastle(unit *Unit) {
	castle := m.castle(m.opponentID(unit.FactionID))
	if castle == nil {
		return
	}
	goals := make(map[Position]bool)
	for y := 0; y < m.scenario.Map.Height; y++ {
		for x := 0; x < m.scenario.Map.Width; x++ {
			position := Position{X: x, Y: y}
			if m.scenario.Map.Traversable(position) && HexDistance(position, castle.Position) <= unit.Range {
				goals[position] = true
			}
		}
	}
	occupied := m.occupied()
	delete(occupied, unit.Position)
	path := shortestPath(m.scenario.Map, unit.Position, goals, occupied)
	steps := min(unit.Movement, len(path))
	if steps > 0 {
		unit.Position = path[steps-1]
	}
}

func (m *Match) damage(attacker Unit, defense int64, attackerTactic, targetTactic string) int64 {
	value := attacker.Attack - defense
	if value < 0 {
		value = 0
	}
	if attackerTactic == ActionDefend {
		value = value * m.scenario.Ruleset.Combat.DefendAttackPercent / 100
	}
	if targetTactic == ActionDefend {
		value = value * m.scenario.Ruleset.Combat.DefendDamageTakenPercent / 100
	}
	return max(value, m.scenario.Ruleset.Combat.MinimumDamage)
}

func (m *Match) projectedPower(id string) int64 {
	var value int64
	for _, unit := range m.state.Units {
		if unit.FactionID == id && unit.Health > 0 {
			value += unit.Attack
		}
	}
	return value
}
