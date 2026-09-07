// Checks units find alternative enemy approaches without crossing blocked cells or
// targeting friendly/destroyed entities.

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
		m.moveTowardCastle(&m.state.Units[index])
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

func TestBlockedUnitCannotWalkThroughNeighbors(t *testing.T) {
	m, err := NewMatch("surrounded", testScenario(), 100)
	if err != nil {
		t.Fatal(err)
	}
	unit := Unit{ID: "surrounded", FactionID: "faction-a", Position: Position{X: 4, Y: 5}, Health: 70, Range: 1, Movement: 2}
	m.state.Units = []Unit{unit}
	for i, p := range Neighbors(unit.Position) {
		m.state.Units = append(m.state.Units, Unit{ID: fmt.Sprintf("ally-%d", i), FactionID: unit.FactionID, Position: p, Health: 70})
	}
	m.moveTowardCastle(&m.state.Units[0])
	if m.state.Units[0].Position != unit.Position {
		t.Fatal("unit crossed occupied neighbors")
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
