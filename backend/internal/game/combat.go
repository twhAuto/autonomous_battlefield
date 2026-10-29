// Resolves unit turns, movement toward targets, attacks, damage, and loot within the
// deterministic simulation.

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

func (m *Match) runUnit(unit *Unit, tactics map[string]string) ([]Event, *string) {
	if unit.RepairAmount > 0 {
		return m.runEngineer(unit), nil
	}
	if unit.Scout {
		if events, retreated := m.scoutRetreat(unit); retreated {
			return events, nil
		}
	}
	if unit.HealAmount > 0 {
		return m.runHealer(unit), nil
	}
	if events, moved := m.retreatFromCloseEnemies(unit); moved {
		return events, nil
	}
	if unit.AttackMode == "position" {
		return m.positionAttack(unit, tactics)
	}
	target := m.targetInRange(*unit)
	if target == nil {
		if unit.BuildingPriority {
			return m.moveSiege(unit), nil
		}
		if unit.PreferredTargetClass != "" {
			if events, found := m.huntPreferred(unit); found {
				return events, nil
			}
		}
		return m.moveUnit(unit), nil
	}
	return m.attackTarget(unit, target, tactics)
}

func (m *Match) attackTarget(unit *Unit, target *attackTarget, tactics map[string]string) ([]Event, *string) {
	return m.strike(unit, nil, target, tactics)
}

// strike resolves one attack. source is the attacking building for tower fire, whose
// synthetic attacker unit carries the building's ID, faction and weapon.
func (m *Match) strike(unit *Unit, source *Building, target *attackTarget, tactics map[string]string) ([]Event, *string) {
	attacker := *unit
	if target.building != nil {
		attacker.Attack += unit.BuildingDamageBonus
	}
	if target.unit != nil && unit.BonusAgainstClass != "" && target.unit.Class == unit.BonusAgainstClass {
		attacker.Attack += unit.ClassDamageBonus
	}
	defense := target.defense
	if target.unit != nil {
		defense = max(int64(0), defense-attacker.ArmorPiercing)
		if m.scenario.Map.TerrainAt(target.position) == TerrainForest {
			defense += ForestDefenseBonus
		}
	}
	damage := m.damage(attacker, defense, tactics[unit.FactionID], tactics[target.factionID])
	targetPosition := target.position
	unit.AttackOutcome = "hit"
	unit.AttackTargetID = target.id
	unit.AttackTargetPosition = &targetPosition
	actual := min(damage, target.health())
	target.applyDamage(actual)
	m.factions[unit.FactionID].damageDealt += actual
	m.factions[target.factionID].damageTaken += actual
	m.recordStrike(unit, source, target, actual)
	events := []Event{m.event("unit_attacked", map[string]any{"attackerId": unit.ID, "targetId": target.id, "targetType": target.kind, "damage": actual, "remainingHealth": target.health(), "attackMode": effectiveAttackMode(unit.AttackMode), "outcome": "hit", "impactPosition": targetPosition})}
	if target.health() > 0 {
		return events, nil
	}
	if target.kind == BuildingCastle {
		data := killerData(unit, source)
		data["castleId"], data["factionId"], data["destroyedByFactionId"] = target.id, target.factionID, unit.FactionID
		events = append(events, m.event("castle_destroyed", data))
		winner := unit.FactionID
		return events, &winner
	}
	events = append(events, m.destroyTargetEvent(unit, source, target))
	if lootEvent, ok := m.collectLoot(unit.FactionID, target); ok {
		events = append(events, lootEvent)
	}
	return events, nil
}

func (m *Match) destroyTargetEvent(attacker *Unit, source *Building, target *attackTarget) Event {
	data := killerData(attacker, source)
	data["factionId"] = target.factionID
	if target.kind == "unit" {
		m.factionState(target.factionID).UnitsLost++
		data["unitId"], data["unitType"] = target.id, target.unit.Type
		return m.event("unit_destroyed", data)
	}
	target.building.TrainingQueue = nil
	target.building.TrainingBlocked = false
	m.factionState(attacker.FactionID).BuildingsDestroyed++
	data["buildingId"], data["buildingType"] = target.id, target.kind
	return m.event("building_destroyed", data)
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
	m.state.UnitDeaths = nil
	for i := range m.state.Buildings {
		m.state.Buildings[i].AttackOutcome = ""
		m.state.Buildings[i].AttackTargetID = ""
		m.state.Buildings[i].AttackTargetPosition = nil
	}
	for index := range m.state.Units {
		m.state.Units[index].HealTargetID = ""
		m.state.Units[index].HealTargetPosition = nil
		m.state.Units[index].MovedFrom = nil
		m.state.Units[index].MovementPath = nil
		m.state.Units[index].AttackOutcome = ""
		m.state.Units[index].AttackTargetID = ""
		m.state.Units[index].AttackTargetPosition = nil
	}
}

func (m *Match) removeDestroyedUnits() {
	survivors := make([]Unit, 0, len(m.state.Units))
	for _, unit := range m.state.Units {
		if unit.Health > 0 {
			survivors = append(survivors, unit)
		} else {
			m.state.UnitDeaths = append(m.state.UnitDeaths, UnitDeath{ID: unit.ID, FactionID: unit.FactionID, Type: unit.Type, Position: unit.Position})
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
	return m.targetAt(attacker, nil)
}

func (m *Match) targetAt(attacker Unit, position *Position) *attackTarget {
	candidates := make([]*attackTarget, 0)
	for index := range m.state.Units {
		unit := &m.state.Units[index]
		if unit.FactionID != attacker.FactionID && unit.Health > 0 && inAttackRange(attacker, unit.Position) && (position == nil || unit.Position == *position) {
			candidates = append(candidates, &attackTarget{id: unit.ID, kind: "unit", factionID: unit.FactionID, position: unit.Position, defense: unit.Defense, cost: unit.Cost, unit: unit})
		}
	}
	for index := range m.state.Buildings {
		building := &m.state.Buildings[index]
		if building.FactionID != attacker.FactionID && building.Health > 0 && inAttackRange(attacker, building.Position) && (position == nil || building.Position == *position) {
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
	if attacker.PreferredTargetClass != "" {
		l := left.unit != nil && left.unit.Class == attacker.PreferredTargetClass
		r := right.unit != nil && right.unit.Class == attacker.PreferredTargetClass
		if l != r {
			return l
		}
	}
	priority := attackTargetPriority
	if attacker.BuildingPriority {
		priority = siegePriority
	}
	if priority(left.kind) != priority(right.kind) {
		return priority(left.kind) < priority(right.kind)
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
		if unit.FactionID == id && unit.Health > 0 && unit.HealAmount == 0 && unit.RepairAmount == 0 {
			value += unit.Attack
		}
	}
	return value
}

func inAttackRange(attacker Unit, position Position) bool {
	distance := HexDistance(attacker.Position, position)
	return distance >= attacker.MinimumRange && distance <= attacker.Range
}

// Aims contain coordinates, never live target pointers that could move before impact.
func (m *Match) captureAttackAims() {
	m.attackAims = make(map[string]Position)
	for _, unit := range m.state.Units {
		if unit.AttackMode == "position" && unit.Health > 0 && unit.ReadyAtTick <= m.state.Tick {
			if target := m.targetInRange(unit); target != nil {
				m.attackAims[unit.ID] = target.position
			}
		}
	}
}

func effectiveAttackMode(mode string) string {
	if mode == "" {
		return "tracking"
	}
	return mode
}

func (m *Match) positionAttack(unit *Unit, tactics map[string]string) ([]Event, *string) {
	position, aimed := m.attackAims[unit.ID]
	if !aimed {
		if unit.BuildingPriority {
			return m.moveSiege(unit), nil
		}
		if unit.PreferredTargetClass != "" {
			if events, found := m.huntPreferred(unit); found {
				return events, nil
			}
		}
		return m.moveUnit(unit), nil
	}
	if target := m.targetAt(*unit, &position); target != nil {
		return m.attackTarget(unit, target, tactics)
	}
	unit.AttackTargetPosition = &position
	unit.AttackTargetID = ""
	unit.AttackOutcome = "miss"
	return []Event{m.event("unit_attack_missed", map[string]any{"attackerId": unit.ID, "attackMode": "position", "outcome": "miss", "impactPosition": position})}, nil
}
