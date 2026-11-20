package game

import (
	"slices"
	"testing"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/game/controller"
)

// contestedMatch is goldMineMatch under contested territory, with faction-a's castle
// claiming range 3 and an active faction-b Outpost at (7,5) claiming range 5.
func contestedMatch(t *testing.T) *Match {
	t.Helper()
	m := goldMineMatch(t)
	m.scenario.Ruleset.Buildings.ContestedTerritory = true
	m.castle("faction-a").BuildRange = 3
	m.state.Buildings = append(m.state.Buildings, Building{ID: "faction-b:outpost:900", FactionID: "faction-b", Type: BuildingOutpost, Position: Position{X: 7, Y: 5}, Health: 50, MaxHealth: 50, BuildRange: 5})
	return m
}

func legalSite(sites []controller.Coordinate, site Position) bool {
	return slices.Contains(sites, controller.Coordinate{X: site.X, Y: site.Y})
}

func TestContestedTerritoryClosesOverlappingGround(t *testing.T) {
	m := contestedMatch(t)
	gold := m.factionState("faction-a").Gold
	contested, mine, enemyOnly := Position{X: 3, Y: 5}, Position{X: 3, Y: 3}, Position{X: 5, Y: 5}
	if _, reason := m.buildStructureAt("faction-a", BuildingTower, contested, 0); reason != "contested_territory" {
		t.Fatalf("tower on contested ground: reason %q", reason)
	}
	if _, reason := m.buildStructureAt("faction-a", BuildingEconomy, mine, 0); reason != "contested_territory" {
		t.Fatalf("economy on a contested mine: reason %q", reason)
	}
	// Ground only the enemy claims is outside the builder's range, not contested.
	if _, reason := m.buildStructureAt("faction-a", BuildingTower, enemyOnly, 0); reason != "outside_build_range" {
		t.Fatalf("enemy-only ground: reason %q", reason)
	}
	if m.factionState("faction-a").Gold != gold {
		t.Fatal("rejected placements must be free")
	}
	if legalSite(m.legalConstructionSites("faction-a"), contested) || legalSite(m.legalGoldMineSites("faction-a"), mine) {
		t.Fatal("legal sites must exclude contested ground")
	}
	if !legalSite(m.legalConstructionSites("faction-a"), Position{X: 1, Y: 7}) {
		t.Fatal("uncontested home ground must stay legal")
	}
}

func TestOnlyLivingActiveBuildingsContestGround(t *testing.T) {
	site := Position{X: 3, Y: 5}
	for name, change := range map[string]func(*Match){
		"rule off":           func(m *Match) { m.scenario.Ruleset.Buildings.ContestedTerritory = false },
		"under construction": func(m *Match) { m.state.Buildings[len(m.state.Buildings)-1].ReadyAtTick = m.state.Tick + 1 },
		"destroyed":          func(m *Match) { m.state.Buildings[len(m.state.Buildings)-1].Health = 0 },
	} {
		m := contestedMatch(t)
		change(m)
		if !legalSite(m.legalConstructionSites("faction-a"), site) {
			t.Fatalf("%s: site must be legal", name)
		}
		if _, reason := m.buildStructureAt("faction-a", BuildingTower, site, 0); reason != "" {
			t.Fatalf("%s: tower rejected: %s", name, reason)
		}
	}
}

func TestContestedTerritoryBindsBothSides(t *testing.T) {
	m := contestedMatch(t)
	m.factionState("faction-b").Gold = 1000
	// The hex closed to faction-a is closed to faction-b too.
	if _, reason := m.buildStructureAt("faction-b", BuildingTower, Position{X: 3, Y: 5}, 0); reason != "contested_territory" {
		t.Fatalf("faction-b on contested ground: reason %q", reason)
	}
	if _, reason := m.buildStructureAt("faction-b", BuildingTower, Position{X: 9, Y: 5}, 0); reason != "" {
		t.Fatalf("faction-b on its own ground: %s", reason)
	}
}
