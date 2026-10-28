package game

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
)

// goldMineMatch has two mines per side, one inside castle range and one beyond it,
// a 300-gold map reserve over a 900-gold ruleset default, and no building limits.
func goldMineMatch(t *testing.T) *Match {
	t.Helper()
	s := specializedMatch(t).Scenario()
	s.Map = Map{Kind: "authored", Width: 15, Height: 11, GoldMineReserve: 300, CastleSpawns: [2]Position{{X: 1, Y: 5}, {X: 13, Y: 5}}, Rows: []string{
		"...............",
		"...............",
		"...............",
		"...g.......g...",
		"...............",
		"...............",
		"...............",
		"......g.g......",
		"...............",
		"...............",
		"...............",
	}}
	s.Ruleset.Buildings.GoldMineReserve = 900
	s.Ruleset.Buildings.MaximumEconomyCount = 0
	s.Ruleset.Buildings.MaximumBuildingCount = 0
	s.Ruleset.Structures = append(s.Ruleset.Structures,
		BuildingType{ID: BuildingTower, Kind: BuildingTower, Health: 130, Defense: 4, Cost: 60, BuildRange: 3, Attack: 15, Range: 3},
		BuildingType{ID: BuildingOutpost, Kind: BuildingOutpost, Health: 50, Defense: 1, Cost: 30, BuildRange: 5})
	m, err := NewMatch("gold-mines", s, 100)
	if err != nil {
		t.Fatal(err)
	}
	m.state.Tick = 1
	m.factionState("faction-a").Gold = 1000
	return m
}

func TestGoldMinesStartFullFromTheMapReserve(t *testing.T) {
	m := goldMineMatch(t)
	want := []GoldMine{
		{Position: Position{X: 3, Y: 3}, Reserve: 300, Remaining: 300},
		{Position: Position{X: 11, Y: 3}, Reserve: 300, Remaining: 300},
		{Position: Position{X: 6, Y: 7}, Reserve: 300, Remaining: 300},
		{Position: Position{X: 8, Y: 7}, Reserve: 300, Remaining: 300},
	}
	if !reflect.DeepEqual(m.state.GoldMines, want) {
		t.Fatalf("mines = %+v", m.state.GoldMines)
	}
	s := m.Scenario()
	s.Map.GoldMineReserve = 0
	fallback, err := NewMatch("default-reserve", s, 100)
	if err != nil {
		t.Fatal(err)
	}
	if fallback.state.GoldMines[0].Reserve != 900 {
		t.Fatal("map without a reserve must use the ruleset default")
	}
	legacy := specializedMatch(t)
	if legacy.state.GoldMines != nil {
		t.Fatal("rulesets without gold mines must not track mines")
	}
	if s.Map.GoldMineReserve = -1; ValidateMap(s.Map) == nil {
		t.Fatal("negative map reserve accepted")
	}
}

func TestGoldMinePlacementRules(t *testing.T) {
	m := goldMineMatch(t)
	gold := m.factionState("faction-a").Gold
	for _, test := range []struct {
		kind   string
		site   Position
		reason string
	}{
		{BuildingEconomy, Position{X: 3, Y: 6}, "requires_gold_mine"},
		{BuildingTower, Position{X: 3, Y: 3}, "gold_mine_reserved"},
		{BuildingTower, Position{X: 2, Y: 3}, "beside_gold_mine"},
	} {
		if _, reason := m.buildStructureAt("faction-a", test.kind, test.site, 0); reason != test.reason {
			t.Fatalf("%s at %v: reason %q, want %q", test.kind, test.site, reason, test.reason)
		}
	}
	if m.factionState("faction-a").Gold != gold {
		t.Fatal("rejected placements must be free")
	}
	if _, reason := m.buildStructureAt("faction-a", BuildingEconomy, Position{X: 3, Y: 3}, 0); reason != "" {
		t.Fatalf("economy on a mine rejected: %s", reason)
	}
	m.state.Buildings = slices.DeleteFunc(m.state.Buildings, func(b Building) bool { return b.Type == BuildingEconomy })
	m.goldMineAt(Position{X: 3, Y: 3}).Remaining = 0
	if _, reason := m.buildStructureAt("faction-a", BuildingEconomy, Position{X: 3, Y: 3}, 0); reason != "gold_mine_depleted" {
		t.Fatalf("depleted mine reason %q", reason)
	}
	if _, reason := m.buildStructureAt("faction-a", BuildingTower, Position{X: 2, Y: 3}, 0); reason != "" {
		t.Fatalf("an empty mine must free its neighbors: %s", reason)
	}
	setup := m.scenario.Factions[0]
	for _, order := range []controller.ConstructionOrder{
		{BuildingType: BuildingEconomy, Position: controller.Coordinate{X: 3, Y: 6}},
		{BuildingType: BuildingTower, Position: controller.Coordinate{X: 6, Y: 7}},
	} {
		orders := []controller.ConstructionOrder{order}
		if err := validateProductionSites(m.scenario, setup, controller.PlanProposal{ConstructionOrders: &orders}); !errors.Is(err, controller.ErrInvalidPlan) {
			t.Fatalf("static validation accepted %+v: %v", order, err)
		}
	}
}

func TestEconomyBuildingsDrainTheirMine(t *testing.T) {
	m := goldMineMatch(t)
	if _, reason := m.buildStructureAt("faction-a", BuildingEconomy, Position{X: 3, Y: 3}, 0); reason != "" {
		t.Fatal(reason)
	}
	mine := m.goldMineAt(Position{X: 3, Y: 3})
	mine.Remaining = 25
	castle := m.castle("faction-a")
	if m.incomeFor("faction-a", castle.Income) != castle.Income+15 || mine.Remaining != 25 {
		t.Fatal("projection must report the draw without depleting the mine")
	}
	before := m.factionState("faction-a").Gold
	events := m.runFactionEconomy("faction-a", false)
	if m.factionState("faction-a").Gold != before+castle.Income+15 || mine.Remaining != 10 || len(events) != 1 {
		t.Fatalf("first draw: gold %d mine %d events %+v", m.factionState("faction-a").Gold, mine.Remaining, events)
	}
	events = m.runFactionEconomy("faction-a", false)
	if mine.Remaining != 0 || len(events) != 2 || events[1].Type != "gold_mine_depleted" || events[0].Data["amount"] != castle.Income+10 {
		t.Fatalf("final draw must be capped at the gold left and announce depletion: %+v", events)
	}
	events = m.runFactionEconomy("faction-a", false)
	if len(events) != 1 || events[0].Data["amount"] != castle.Income {
		t.Fatalf("a depleted mine must earn nothing: %+v", events)
	}
}

func TestDestroyedEconomyLeavesTheMineForEitherFaction(t *testing.T) {
	m := goldMineMatch(t)
	m.state.Buildings = append(m.state.Buildings, Building{ID: "faction-a:outpost:900", FactionID: "faction-a", Type: BuildingOutpost, Position: Position{X: 4, Y: 9}, Health: 50, BuildRange: 5})
	if _, reason := m.buildStructureAt("faction-a", BuildingEconomy, Position{X: 6, Y: 7}, 0); reason != "" {
		t.Fatal(reason)
	}
	m.runFactionEconomy("faction-a", false)
	remaining := m.goldMineAt(Position{X: 6, Y: 7}).Remaining
	if remaining != 285 {
		t.Fatalf("remaining %d", remaining)
	}
	m.state.Buildings[len(m.state.Buildings)-1].Health = 0
	m.factionState("faction-b").Gold = 1000
	m.state.Buildings = append(m.state.Buildings, Building{ID: "faction-b:outpost:900", FactionID: "faction-b", Type: BuildingOutpost, Position: Position{X: 9, Y: 9}, Health: 50, BuildRange: 5})
	if _, reason := m.buildStructureAt("faction-b", BuildingEconomy, Position{X: 6, Y: 7}, 0); reason != "" {
		t.Fatalf("enemy could not take the freed mine: %s", reason)
	}
	m.runFactionEconomy("faction-b", false)
	if m.goldMineAt(Position{X: 6, Y: 7}).Remaining != remaining-15 {
		t.Fatal("the new owner must continue from the remaining gold")
	}
}

func TestGoldMinesSurviveCheckpointsAndClones(t *testing.T) {
	m := goldMineMatch(t)
	m.goldMineAt(Position{X: 3, Y: 3}).Remaining = 7
	restored, err := RestoreMatch(m.id, m.Scenario(), m.maxTicks, m.Checkpoint())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(restored.state.GoldMines, m.state.GoldMines) {
		t.Fatal("checkpoint lost mine reserves")
	}
	clone := m.State()
	clone.GoldMines[0].Remaining = 1
	if m.state.GoldMines[0].Remaining != 7 {
		t.Fatal("state clone shares mines")
	}
}

func TestDecisionSnapshotSeparatesMineAndGroundSites(t *testing.T) {
	m := goldMineMatch(t)
	m.state.OpeningComplete = true
	snapshot, err := m.DecisionSnapshot("faction-a")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot.LegalGoldMineSites, []controller.Coordinate{{X: 3, Y: 3}}) {
		t.Fatalf("mine sites %+v", snapshot.LegalGoldMineSites)
	}
	if slices.Contains(snapshot.LegalSites, controller.Coordinate{X: 3, Y: 3}) || slices.Contains(snapshot.LegalSites, controller.Coordinate{X: 2, Y: 3}) || len(snapshot.LegalSites) == 0 {
		t.Fatal("ground sites must exclude mines and their neighbors")
	}
	if snapshot.MaximumEconomy != 0 || snapshot.MaximumBuildings != 0 || !slices.ContainsFunc(snapshot.LegalBuildings, func(b BuildingType) bool { return b.ID == BuildingEconomy }) {
		t.Fatal("gold mines offer unlimited economy buildings")
	}
	if len(snapshot.View.State.GoldMines) != 4 {
		t.Fatal("observations must include mine reserves")
	}
}

func TestScriptedConstructionFollowsTheGold(t *testing.T) {
	m := goldMineMatch(t)
	orders := m.defaultConstructionOrders(0)
	if len(orders) == 0 || orders[0].BuildingType != BuildingEconomy || orders[0].Position != (controller.Coordinate{X: 3, Y: 3}) {
		t.Fatalf("economy must take the home mine first: %+v", orders)
	}
	for _, order := range orders[1:] {
		if site := (Position{X: order.Position.X, Y: order.Position.Y}); m.besideLiveMine(site) || m.scenario.Map.TerrainAt(site) == TerrainGold {
			t.Fatalf("%s blocks a mine at %v", order.BuildingType, site)
		}
	}
	// With the home mine spent, the forward mines are out of reach: an Outpost moves
	// toward the nearest one.
	m.goldMineAt(Position{X: 3, Y: 3}).Remaining = 0
	orders = m.defaultConstructionOrders(0)
	outpost := orders[len(orders)-1]
	target := Position{X: 6, Y: 7}
	if outpost.BuildingType != BuildingOutpost || HexDistance(Position{X: outpost.Position.X, Y: outpost.Position.Y}, target) >= HexDistance(m.castle("faction-a").Position, target) {
		t.Fatalf("no expansion toward the free mine: %+v", orders)
	}
	m.state.Buildings = append(m.state.Buildings, Building{ID: "faction-a:outpost:900", FactionID: "faction-a", Type: BuildingOutpost, Position: Position{X: outpost.Position.X, Y: outpost.Position.Y}, Health: 50, BuildRange: 5})
	orders = m.defaultConstructionOrders(0)
	if orders[0].BuildingType != BuildingEconomy || orders[0].Position != (controller.Coordinate{X: 6, Y: 7}) {
		t.Fatalf("economy must take the mine the Outpost reached: %+v", orders)
	}
}

func TestScriptedProductionFitsOnePlan(t *testing.T) {
	m := goldMineMatch(t)
	for i := 0; i < controller.MaximumProductionOrders+3; i++ {
		m.state.Buildings = append(m.state.Buildings, Building{ID: "faction-a:barracks:" + string(rune('a'+i)), FactionID: "faction-a", Type: BuildingBarracks, Trains: []string{"vanguard"}, Position: Position{X: i % 15, Y: 10}, Health: 100})
	}
	plan := m.scriptedProductionPlan("faction-a", controller.PlanProposal{RecruitmentPriorities: []string{"vanguard"}})
	if len(*plan.ProductionOrders) != controller.MaximumProductionOrders {
		t.Fatalf("%d production orders", len(*plan.ProductionOrders))
	}
}
