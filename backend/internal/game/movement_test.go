// Checks units find alternative enemy approaches, pass allies without stacking,
// never cross enemies, and never target friendly/destroyed entities.

package game

import (
	"fmt"
	"testing"
)

func TestUnitsAdvanceWhenCastleApproachesAreOccupied(t *testing.T) {
	m, err := NewMatch("blocked-castle", testScenario(), 100)
	if err != nil {
		t.Fatal(err)
	}
	m.state.Units = nil
	castle := m.castle("faction-b")
	for i, p := range Neighbors(castle.Position) {
		m.state.Units = append(m.state.Units, Unit{ID: fmt.Sprintf("guard-%d", i), FactionID: "faction-b", Position: p, Health: 70, Attack: 9, Defense: 7, Movement: 1, Range: 1})
	}
	attacker := Unit{ID: "attacker", FactionID: "faction-a", Position: Position{X: 8, Y: 5}, Health: 70, Attack: 9, Movement: 1, Range: 1}
	m.state.Units = append(m.state.Units, attacker)
	index := len(m.state.Units) - 1
	occupied := m.occupied()
	delete(occupied, attacker.Position)
	for range 6 {
		m.moveUnit(&m.state.Units[index])
		if m.targetInRange(m.state.Units[index]) != nil {
			break
		}
	}
	moved := m.state.Units[index]
	if moved.Position == attacker.Position {
		t.Fatal("unit stalls when castle attack positions are occupied")
	}
	if occupied[moved.Position] || !m.scenario.Map.Traversable(moved.Position) {
		t.Fatal("movement entered an occupied or blocked hex")
	}
	if m.targetInRange(moved) == nil {
		t.Fatal("unit never reached a blocker to attack")
	}
}

func surroundedMatch(t *testing.T, neighborFaction string, movement int) (*Match, Unit) {
	t.Helper()
	m, err := NewMatch("surrounded", testScenario(), 100)
	if err != nil {
		t.Fatal(err)
	}
	unit := Unit{ID: "surrounded", FactionID: "faction-a", Position: Position{X: 4, Y: 5}, Health: 70, Range: 1, Movement: movement}
	m.state.Units = []Unit{unit}
	for i, p := range Neighbors(unit.Position) {
		m.state.Units = append(m.state.Units, Unit{ID: fmt.Sprintf("neighbor-%d", i), FactionID: neighborFaction, Position: p, Health: 70})
	}
	return m, unit
}

func TestUnitPassesThroughAlliesWithoutStacking(t *testing.T) {
	m, unit := surroundedMatch(t, "faction-a", 2)
	occupied := m.occupied()
	m.moveUnit(&m.state.Units[0])
	moved := m.state.Units[0]
	if moved.Position == unit.Position {
		t.Fatal("allies blocked a unit that can step past them")
	}
	if occupied[moved.Position] || HexDistance(moved.Position, unit.Position) != 2 {
		t.Fatalf("unit ended on an occupied or unreachable hex %+v", moved.Position)
	}
}

func TestUnitNeverEndsOnAnAlly(t *testing.T) {
	m, unit := surroundedMatch(t, "faction-a", 1)
	m.moveUnit(&m.state.Units[0])
	if m.state.Units[0].Position != unit.Position {
		t.Fatal("unit stacked on an ally")
	}
}

func TestEnemiesStillBlockMovement(t *testing.T) {
	m, unit := surroundedMatch(t, "faction-b", 2)
	m.moveUnit(&m.state.Units[0])
	if m.state.Units[0].Position != unit.Position {
		t.Fatal("unit crossed enemy neighbors")
	}
}

func TestEnemyApproachesExcludeFriendlyAndDestroyedEntities(t *testing.T) {
	m, err := NewMatch("no-enemy", testScenario(), 100)
	if err != nil {
		t.Fatal(err)
	}
	unit := Unit{FactionID: "faction-a", Range: 1}
	m.state.Units = []Unit{{FactionID: "faction-a", Health: 70, Position: Position{X: 4, Y: 5}}, {FactionID: "faction-b", Health: 0, Position: Position{X: 6, Y: 5}}}
	m.state.Buildings = nil
	if len(m.enemyApproachGoals(unit)) != 0 {
		t.Fatal("friendly or destroyed unit became a movement target")
	}
}
