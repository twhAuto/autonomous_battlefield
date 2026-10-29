package game

import (
	"slices"
	"testing"
)

// turnOrderMatch seats zulu first and alpha second, so seat order and ID order differ.
// One alpha unit is dead and one zulu unit is still training.
func turnOrderMatch(alternating bool, tick uint64) *Match {
	unit := func(id, faction string, speed int64) Unit {
		return Unit{ID: id, FactionID: faction, Speed: speed, Health: 10}
	}
	units := []Unit{
		unit("alpha:vanguard:001", "alpha", 4), unit("alpha:vanguard:002", "alpha", 4), unit("alpha:vanguard:003", "alpha", 4),
		unit("alpha:vanguard:004", "alpha", 4), unit("zulu:striker:001", "zulu", 8), unit("zulu:vanguard:001", "zulu", 4),
		unit("zulu:vanguard:002", "zulu", 4), unit("zulu:vanguard:003", "zulu", 4),
	}
	units[3].Health = 0
	units[7].ReadyAtTick = tick + 1
	buildings := []Building{{ID: "alpha:tower:001", FactionID: "alpha"}, {ID: "zulu:tower:001", FactionID: "zulu"}, {ID: "alpha:barracks:002", FactionID: "alpha"}}
	return &Match{
		scenario: Scenario{Factions: []FactionSetup{{ID: "zulu"}, {ID: "alpha"}}, Ruleset: Ruleset{AlternatingTurns: alternating}},
		state:    State{Tick: tick, Units: units, Buildings: buildings},
	}
}

func unitIDs(m *Match, indices []int) []string {
	ids := []string{}
	for _, index := range indices {
		ids = append(ids, m.state.Units[index].ID)
	}
	return ids
}

func buildingIDs(m *Match) []string {
	indices := []int{0, 1, 2}
	m.sortBuildings(indices)
	ids := []string{}
	for _, index := range indices {
		ids = append(ids, m.state.Buildings[index].ID)
	}
	return ids
}

// Alternating turns go by seat, the first seat changing every tick: equal-speed units
// of the two factions take turns, and units that cannot act get no turn.
func TestAlternatingTurnsOrderBySeatNotFactionID(t *testing.T) {
	odd := turnOrderMatch(true, 1)
	if got, want := unitIDs(odd, odd.unitTurnOrder()), []string{"zulu:striker:001", "zulu:vanguard:001", "alpha:vanguard:001", "zulu:vanguard:002", "alpha:vanguard:002", "alpha:vanguard:003"}; !slices.Equal(got, want) {
		t.Fatalf("odd tick units = %v, want %v", got, want)
	}
	if got, want := buildingIDs(odd), []string{"zulu:tower:001", "alpha:barracks:002", "alpha:tower:001"}; !slices.Equal(got, want) {
		t.Fatalf("odd tick buildings = %v, want %v", got, want)
	}
	even := turnOrderMatch(true, 2)
	if got, want := unitIDs(even, even.unitTurnOrder()), []string{"zulu:striker:001", "alpha:vanguard:001", "zulu:vanguard:001", "alpha:vanguard:002", "zulu:vanguard:002", "alpha:vanguard:003"}; !slices.Equal(got, want) {
		t.Fatalf("even tick units = %v, want %v", got, want)
	}
	if got, want := even.factionOrder(), []string{"alpha", "zulu"}; !slices.Equal(got, want) {
		t.Fatalf("even tick factions = %v, want %v", got, want)
	}
}

// Without alternating turns, frozen battles keep ID order: the faction whose ID sorts
// first acts first on ties, and the economy runs in seat order.
func TestLegacyTurnsOrderByID(t *testing.T) {
	m := turnOrderMatch(false, 2)
	if got, want := unitIDs(m, m.unitTurnOrder()), []string{"zulu:striker:001", "alpha:vanguard:001", "alpha:vanguard:002", "alpha:vanguard:003", "alpha:vanguard:004", "zulu:vanguard:001", "zulu:vanguard:002", "zulu:vanguard:003"}; !slices.Equal(got, want) {
		t.Fatalf("units = %v, want %v", got, want)
	}
	if got, want := buildingIDs(m), []string{"alpha:barracks:002", "alpha:tower:001", "zulu:tower:001"}; !slices.Equal(got, want) {
		t.Fatalf("buildings = %v, want %v", got, want)
	}
	if got, want := m.factionOrder(), []string{"zulu", "alpha"}; !slices.Equal(got, want) {
		t.Fatalf("factions = %v, want %v", got, want)
	}
}
