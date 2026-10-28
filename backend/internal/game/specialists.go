// Implements reconnaissance, visible siege pursuit and bounded field repairs.
package game

import "sort"

func (m *Match) scoutRetreat(unit *Unit) ([]Event, bool) {
	threats := []Position{}
	for _, enemy := range m.state.Units {
		if enemy.Health > 0 && enemy.FactionID != unit.FactionID {
			threats = append(threats, enemy.Position)
		}
	}
	goals := targetsAsGoals(threats)
	if len(threats) == 0 || nearestDistance(unit.Position, goals) > 2 {
		return nil, false
	}
	return m.retreatAlongReachablePath(unit, goals)
}

func (m *Match) huntPreferred(unit *Unit) ([]Event, bool) {
	candidates := []Unit{}
	for _, enemy := range m.state.Units {
		if enemy.Health > 0 && enemy.FactionID != unit.FactionID && enemy.Class == unit.PreferredTargetClass {
			candidates = append(candidates, enemy)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		da, db := HexDistance(unit.Position, a.Position), HexDistance(unit.Position, b.Position)
		if da != db {
			return da < db
		}
		return a.ID < b.ID
	})
	for _, enemy := range candidates {
		if path, ok := m.routeTo(*unit, m.positionsInRange(enemy.Position, max(1, unit.MinimumRange), unit.Range)); ok {
			return m.moveAlong(unit, path), true
		}
	}
	return nil, false
}

type repairTarget struct {
	id                string
	position          Position
	health, maxHealth *int64
}

func (m *Match) repairTargets(unit Unit) []repairTarget {
	targets := []repairTarget{}
	for i := range m.state.Units {
		u := &m.state.Units[i]
		if u.Health > 0 && u.FactionID == unit.FactionID && u.Class == "siege" && u.Health < u.MaxHealth {
			targets = append(targets, repairTarget{u.ID, u.Position, &u.Health, &u.MaxHealth})
		}
	}
	for i := range m.state.Buildings {
		b := &m.state.Buildings[i]
		if b.Health > 0 && b.FactionID == unit.FactionID && b.Type != BuildingCastle && b.Health < b.MaxHealth && b.ReadyAtTick <= m.state.Tick {
			targets = append(targets, repairTarget{b.ID, b.Position, &b.Health, &b.MaxHealth})
		}
	}
	sort.Slice(targets, func(i, j int) bool {
		a, b := targets[i], targets[j]
		da, db := HexDistance(unit.Position, a.position), HexDistance(unit.Position, b.position)
		if da != db {
			return da < db
		}
		return a.id < b.id
	})
	return targets
}

func (m *Match) runEngineer(unit *Unit) []Event {
	if unit.RepairCharges <= 0 {
		return nil
	}
	for _, enemy := range m.state.Units {
		if enemy.Health > 0 && enemy.FactionID != unit.FactionID && HexDistance(unit.Position, enemy.Position) <= 1 {
			return nil
		}
	}
	state := m.factionState(unit.FactionID)
	if state.Gold-unit.RepairCost < m.factions[unit.FactionID].activePlan.ReserveGold {
		return nil
	}
	for _, target := range m.repairTargets(*unit) {
		if HexDistance(unit.Position, target.position) <= 1 {
			amount := min(unit.RepairAmount, *target.maxHealth-*target.health)
			*target.health += amount
			m.factions[unit.FactionID].stats.unit(unit.Type).HealthRestored += amount
			unit.RepairCharges--
			state.Gold -= unit.RepairCost
			state.GoldSpent += unit.RepairCost
			// Reuse the support-action marker; event type distinguishes repair from treatment.
			unit.HealTargetID = target.id
			unit.HealTargetPosition = clonePosition(&target.position)
			return []Event{m.event("unit_repaired", map[string]any{"factionId": unit.FactionID, "engineerId": unit.ID, "targetId": target.id, "amount": amount, "cost": unit.RepairCost, "balance": state.Gold, "remainingHealth": *target.health, "repairCharges": unit.RepairCharges})}
		}
		if path, ok := m.routeTo(*unit, m.positionsInRange(target.position, 1, 1)); ok {
			return m.moveAlong(unit, path)
		}
	}
	return m.followAlliedSiege(unit)
}

// With no repair target, follow an allied siege engine, using stable IDs.
func (m *Match) followAlliedSiege(unit *Unit) []Event {
	siege := []Unit{}
	for _, u := range m.state.Units {
		if u.Health > 0 && u.FactionID == unit.FactionID && u.Class == "siege" {
			siege = append(siege, u)
		}
	}
	sort.Slice(siege, func(i, j int) bool { return siege[i].ID < siege[j].ID })
	for _, u := range siege {
		if path, ok := m.routeTo(*unit, m.positionsInRange(u.Position, 1, 1)); ok {
			return m.moveAlong(unit, path)
		}
	}
	return nil
}
