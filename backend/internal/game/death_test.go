package game

import "testing"

func TestConfirmedDeathsAndVisibility(t *testing.T) {
	m := expansionMatch(t)
	live := soldier("live", "faction-a", Position{2, 5})
	dead := soldier("dead", "faction-b", Position{5, 5})
	dead.Health = 0
	m.state.Units = []Unit{live, dead}
	m.removeDestroyedUnits()
	if len(m.state.Units) != 1 || len(m.state.UnitDeaths) != 1 || m.state.UnitDeaths[0].ID != dead.ID || m.state.UnitDeaths[0].Position != dead.Position {
		t.Fatal("incorrect death record")
	}
	snapshot := m.State()
	snapshot.UnitDeaths[0].Position.X = 99
	if m.state.UnitDeaths[0].Position != dead.Position {
		t.Fatal("death records alias live state")
	}
	for _, id := range []string{"faction-a", "faction-b"} {
		view, err := m.View(id)
		if err != nil || len(view.State.UnitDeaths) != 1 {
			t.Fatal("open map lost death", err)
		}
	}
	m.clearUnitActions()
	if len(m.state.UnitDeaths) != 0 {
		t.Fatal("stale death marker")
	}
}
