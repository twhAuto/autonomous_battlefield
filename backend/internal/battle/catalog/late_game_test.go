package catalog

import (
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/twhAuto/autonomous_battlefield/backend/gamedata"
	service "github.com/twhAuto/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/game"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/game/controller"
)

// lateGameArmy is a stalemate setup: both sides own every general upgrade level
// and the same four training buildings. Only the unit types and unlocks differ.
type lateGameArmy struct {
	unlocks    []string
	production map[string]string
}

var (
	classicArmy = lateGameArmy{production: map[string]string{
		game.BuildingBarracks: "vanguard", game.BuildingArcheryRange: "crossbowman", game.BuildingStables: "raider_cavalry", game.BuildingSiegeWorkshop: "catapult",
	}}
	unlockedArmy = lateGameArmy{unlocks: []string{"gunpowder", "knighthood"}, production: map[string]string{
		game.BuildingBarracks: "vanguard", game.BuildingArcheryRange: "gunner", game.BuildingStables: "heavy_cavalry", game.BuildingSiegeWorkshop: "cannon",
	}}
	generalUpgrades = []string{"arsenal", "arsenal_2", "arsenal_3", "plating", "plating_2", "plating_3", "industry", "industry_2", "industry_3"}
)

// Equal treasuries cover the classic tree, so the unlocked side fields the
// 1,800 gold of unlocks instead of extra classic troops under the unit cap. The
// probe builds no economy, so mines become ground: their no-build neighbors would
// otherwise decide where the training buildings stand.
func TestLateGameUnitsBreakUpgradedStalemate(t *testing.T) {
	c, err := LoadCatalog(gamedata.Files)
	if err != nil {
		t.Fatal(err)
	}
	s, err := c.Resolve("frontier-siege", [2]service.FactionIdentity{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}})
	if err != nil {
		t.Fatal(err)
	}
	s.Ruleset.StartingGold = 8000
	s = withoutGoldMines(s)
	control := runLateGameProbe(t, s, classicArmy, classicArmy)
	t.Logf("control classic/classic winner=%s ticks=%d", control.winner, control.ticks)
	if mirror := runLateGameProbe(t, s, unlockedArmy, unlockedArmy); mirror.winner == "draw" {
		t.Fatal("two unlocked armies stalemated")
	} else {
		t.Logf("mirror unlocked/unlocked winner=%s ticks=%d", mirror.winner, mirror.ticks)
	}
	for _, unlockedFirst := range []bool{true, false} {
		armies, unlocked := [2]lateGameArmy{unlockedArmy, classicArmy}, "a"
		if !unlockedFirst {
			armies, unlocked = [2]lateGameArmy{classicArmy, unlockedArmy}, "b"
		}
		result := runLateGameProbe(t, s, armies[0], armies[1])
		t.Logf("unlocked=%s winner=%s ticks=%d trained=%v", unlocked, result.winner, result.ticks, result.trained)
		if result.winner != unlocked || result.ticks >= control.ticks {
			t.Fatalf("late-game units did not break the stalemate: unlocked=%s winner=%s ticks=%d control=%d", unlocked, result.winner, result.ticks, control.ticks)
		}
		for _, unit := range []string{"cannon", "gunner", "heavy_cavalry"} {
			if result.trained[unlocked][unit] == 0 {
				t.Fatalf("unlocked side never trained %s", unit)
			}
		}
	}
}

// The live scripted plans must save for both unlocks and switch their sources,
// so bot battles reach the late game and still end.
func TestScriptedBotsUnlockAndTrainLateGameUnits(t *testing.T) {
	skipSequentialBattleUnderRace(t)
	c, err := LoadCatalog(gamedata.Files)
	if err != nil {
		t.Fatal(err)
	}
	s, err := c.Resolve("frontier-siege", [2]service.FactionIdentity{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}})
	if err != nil {
		t.Fatal(err)
	}
	m, err := game.NewMatch("scripted-late-game", s, 800)
	if err != nil {
		t.Fatal(err)
	}
	result := lateGameResult{trained: map[string]map[string]int{"a": {}, "b": {}}}
	var last game.Transition
	for !m.Finished() {
		if last, err = m.Advance(); err != nil {
			t.Fatal(err)
		}
		result.countTrained(last.Events)
	}
	t.Logf("ticks=%d trained=%v", last.Tick, result.trained)
	if last.Result.WinnerFactionID == nil {
		t.Fatal("scripted bots stalemated")
	}
	for _, faction := range m.State().Factions {
		if !slices.Contains(faction.OwnedUpgradeIDs, "gunpowder") || !slices.Contains(faction.OwnedUpgradeIDs, "knighthood") {
			t.Fatalf("%s never bought both unlocks: %v", faction.FactionID, faction.OwnedUpgradeIDs)
		}
		if result.trained[faction.FactionID]["gunner"] == 0 || result.trained[faction.FactionID]["heavy_cavalry"] == 0 {
			t.Fatalf("%s never trained late-game units: %v", faction.FactionID, result.trained[faction.FactionID])
		}
	}
}

type lateGameResult struct {
	winner  string
	ticks   uint64
	trained map[string]map[string]int
}

func runLateGameProbe(t *testing.T, s game.Scenario, a, b lateGameArmy) lateGameResult {
	t.Helper()
	m, err := game.NewMatch("late-game", s, 400)
	if err != nil {
		t.Fatal(err)
	}
	armies := map[string]lateGameArmy{"a": a, "b": b}
	result := lateGameResult{winner: "draw", trained: map[string]map[string]int{"a": {}, "b": {}}}
	var last game.Transition
	for window := 0; !m.Finished(); {
		var plans map[string]controller.PlanProposal
		if m.DecisionWindowDue() {
			plans = lateGamePlans(t, m, armies, window)
			window++
		}
		if last, err = m.AdvanceWithPlans(plans); err != nil {
			t.Fatal(err)
		}
		result.countTrained(last.Events)
	}
	result.ticks = last.Tick
	if last.Result.WinnerFactionID != nil {
		result.winner = *last.Result.WinnerFactionID
	}
	return result
}

func (r lateGameResult) countTrained(events []game.Event) {
	for _, e := range events {
		if e.Type == "unit_queued" {
			r.trained[e.Data["factionId"].(string)][e.Data["unitType"].(string)]++
		}
	}
}

func lateGamePlans(t *testing.T, m *game.Match, armies map[string]lateGameArmy, window int) map[string]controller.PlanProposal {
	t.Helper()
	plans := map[string]controller.PlanProposal{}
	for id, army := range armies {
		plans[id] = lateGamePlan(t, m, id, army, window)
	}
	return plans
}

// Window 0 is the opening, window 1 builds and buys upgrades, and window 2
// configures the new buildings. Later windows keep every setting.
func lateGamePlan(t *testing.T, m *game.Match, id string, army lateGameArmy, window int) controller.PlanProposal {
	t.Helper()
	empty := []controller.ConstructionOrder{}
	plan := controller.PlanProposal{Name: "late-game-probe", SpendOrder: []string{"construction", "upgrades"}, ConstructionOrders: &empty, ActionWeights: map[string]int64{"attack": 1000, "defend": 1}}
	switch window {
	case 0:
		orders := []controller.ProductionOrder{{BuildingID: id + ":castle", UnitType: "vanguard", Enabled: true}}
		plan.ProductionOrders, plan.SpendOrder = &orders, []string{"upgrades"}
	case 1:
		construction := trainingSites(t, m, id)
		plan.ConstructionOrders = &construction
		plan.UpgradeOrders = append(append([]string{}, generalUpgrades...), army.unlocks...)
	case 2:
		snapshot, err := m.DecisionSnapshot(id)
		if err != nil {
			t.Fatal(err)
		}
		orders := []controller.ProductionOrder{}
		for _, b := range snapshot.View.State.Buildings {
			if unit, ok := army.production[b.Type]; ok && b.FactionID == id {
				orders = append(orders, controller.ProductionOrder{BuildingID: b.ID, UnitType: unit, Enabled: true})
			}
		}
		plan.ProductionOrders = &orders
	}
	return plan
}

// trainingSites places one of each training building on the legal sites nearest
// faction a's castle, keeping the required two-hex spacing; faction b mirrors them,
// because row-order ties would otherwise pick different ground on each side.
// nearestTrainingSites is only called for faction a, whose enemy is b.
func trainingSites(t *testing.T, m *game.Match, id string) []controller.ConstructionOrder {
	t.Helper()
	orders := nearestTrainingSites(t, m, "a")
	if id == "a" {
		return orders
	}
	snapshot, err := m.DecisionSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	for i := range orders {
		orders[i].Position.X = snapshot.View.Map.Width - 1 - orders[i].Position.X
		if !slices.Contains(snapshot.LegalSites, orders[i].Position) {
			t.Fatalf("mirrored site %v is not legal for %s", orders[i].Position, id)
		}
	}
	return orders
}

func nearestTrainingSites(t *testing.T, m *game.Match, id string) []controller.ConstructionOrder {
	t.Helper()
	snapshot, err := m.DecisionSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	castle, enemy := castlePosition(snapshot.View.State, id), castlePosition(snapshot.View.State, "b")
	sites := append([]controller.Coordinate{}, snapshot.LegalSites...)
	// Ties go toward the enemy so no source trains behind its own castle.
	sort.SliceStable(sites, func(i, j int) bool {
		a, b := sitePosition(sites[i]), sitePosition(sites[j])
		if game.HexDistance(castle, a) != game.HexDistance(castle, b) {
			return game.HexDistance(castle, a) < game.HexDistance(castle, b)
		}
		return game.HexDistance(enemy, a) < game.HexDistance(enemy, b)
	})
	kinds := []string{game.BuildingBarracks, game.BuildingArcheryRange, game.BuildingStables, game.BuildingSiegeWorkshop}
	orders := []controller.ConstructionOrder{}
	for _, site := range sites {
		if len(orders) < len(kinds) && siteSeparated(site, orders) {
			orders = append(orders, controller.ConstructionOrder{BuildingType: kinds[len(orders)], Position: site})
		}
	}
	if len(orders) != len(kinds) {
		t.Fatal("no room for the training buildings")
	}
	return orders
}

func castlePosition(state game.State, id string) game.Position {
	for _, b := range state.Buildings {
		if b.FactionID == id && b.Type == game.BuildingCastle {
			return b.Position
		}
	}
	return game.Position{}
}

func sitePosition(c controller.Coordinate) game.Position { return game.Position{X: c.X, Y: c.Y} }

func siteSeparated(site controller.Coordinate, orders []controller.ConstructionOrder) bool {
	for _, order := range orders {
		if game.HexDistance(sitePosition(site), sitePosition(order.Position)) < 2 {
			return false
		}
	}
	return true
}

func withoutGoldMines(s game.Scenario) game.Scenario {
	s.Ruleset.Buildings.GoldMineReserve, s.Map.GoldMineReserve = 0, 0
	s.Map.Rows = append([]string(nil), s.Map.Rows...)
	for i := range s.Map.Rows {
		s.Map.Rows[i] = strings.ReplaceAll(s.Map.Rows[i], "g", ".")
	}
	return s
}
