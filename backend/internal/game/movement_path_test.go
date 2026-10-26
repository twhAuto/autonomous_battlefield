package game

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestMovementPathRecordsTraversedStepsAndClears(t *testing.T) {
	m := expansionMatch(t)
	unit := soldier("walker", "faction-a", Position{3, 5})
	unit.Movement = 2
	route := []Position{{4, 5}, {4, 4}, {5, 4}}
	m.moveAlong(&unit, route)
	want := []Position{{3, 5}, {4, 5}, {4, 4}}
	if !reflect.DeepEqual(unit.MovementPath, want) || unit.Position != want[2] || *unit.MovedFrom != want[0] {
		t.Fatalf("movement = %+v", unit)
	}
	route[0].X = 99
	if !reflect.DeepEqual(unit.MovementPath, want) {
		t.Fatal("path aliases pathfinding storage")
	}
	m.state.Units = []Unit{unit}
	data, err := json.Marshal(m.State())
	if err != nil {
		t.Fatal(err)
	}
	var restored State
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(restored.Units[0].MovementPath, want) {
		t.Fatal("snapshot lost route")
	}
	copied := CloneState(m.state)
	copied.Units[0].MovementPath[0].X = 99
	view, _ := m.View("faction-a")
	view.State.Units[0].MovementPath[0].X = 99
	if !reflect.DeepEqual(m.state.Units[0].MovementPath, want) {
		t.Fatal("snapshot/view aliases live route")
	}
	m.clearUnitActions()
	if m.state.Units[0].MovementPath != nil || m.state.Units[0].MovedFrom != nil {
		t.Fatal("stale movement")
	}
}

func TestCastleMovementRecordsAdjacentSteps(t *testing.T) {
	m := expansionMatch(t)
	unit := soldier("walker", "faction-a", Position{3, 5})
	unit.Movement = 3
	from := unit.Position
	m.moveUnit(&unit)
	if len(unit.MovementPath) < 2 || unit.MovementPath[0] != from || unit.MovementPath[len(unit.MovementPath)-1] != unit.Position {
		t.Fatalf("missing castle route: %+v", unit)
	}
	for i, step := range unit.MovementPath[1:] {
		if HexDistance(unit.MovementPath[i], step) != 1 || !m.scenario.Map.Traversable(step) {
			t.Fatalf("invalid step %v", step)
		}
	}
}

func TestEnemyRouteIsFullyVisible(t *testing.T) {
	m := expansionMatch(t)
	unit := soldier("enemy", "faction-b", Position{12, 5})
	from := Position{11, 5}
	unit.MovedFrom = &from
	unit.MovementPath = []Position{from, unit.Position}
	m.state.Units = []Unit{unit}
	view, err := m.View("faction-a")
	if err != nil || !reflect.DeepEqual(view.State.Units[0].MovementPath, unit.MovementPath) {
		t.Fatal("open view lost enemy route", err)
	}
}
