// Checks that faction views keep the opponent's economy and production totals private.

package game

import "testing"

func TestFactionViewOmitsOpponentEconomy(t *testing.T) {
	m, err := NewMatch("private-economy", testScenario(), 100)
	if err != nil {
		t.Fatal(err)
	}
	m.factionState("faction-b").Gold = 777
	m.factionState("faction-b").OwnedUpgradeIDs = []string{"arsenal"}

	view, err := m.View("faction-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(view.State.Factions) != 1 || view.State.Factions[0].FactionID != "faction-a" {
		t.Fatalf("faction-a sees faction states %+v, want only its own", view.State.Factions)
	}
	view.State.Factions[0].OwnedUpgradeIDs = append(view.State.Factions[0].OwnedUpgradeIDs, "leaked")
	if len(m.factionState("faction-a").OwnedUpgradeIDs) != 0 {
		t.Fatal("view shares upgrade storage with the live match")
	}

	snapshot, err := m.DecisionSnapshot("faction-a")
	if err != nil {
		t.Fatal(err)
	}
	for _, faction := range snapshot.View.State.Factions {
		if faction.FactionID != "faction-a" {
			t.Fatalf("decision snapshot exposes %s's economy", faction.FactionID)
		}
	}

	spectator, err := ProjectView(m.scenario.Map, m.state, "")
	if err != nil || len(spectator.State.Factions) != 2 {
		t.Fatalf("spectator view factions = %d, err = %v; want both", len(spectator.State.Factions), err)
	}
}
