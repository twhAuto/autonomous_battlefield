package catalog

import (
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/gamedata"
	service "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
)

func TestFrontierThreeLevelCatalogAndScriptedBattle(t *testing.T) {
	c, err := LoadCatalog(gamedata.Files)
	if err != nil {
		t.Fatal(err)
	}
	identities := [2]service.FactionIdentity{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}}
	s, err := c.Resolve("frontier-siege", identities)
	if err != nil {
		t.Fatal(err)
	}
	old := legacyScenario(t, "frontier-siege-v2")
	if len(old.Ruleset.Upgrades) != 3 || old.Ruleset.Upgrades[0].Level != 0 || s.Ruleset.ContentSource.Version != "10" {
		t.Fatal("legacy snapshot changed")
	}
	if old.Map.Width != s.Map.Width || old.Map.Height != s.Map.Height || old.Map.CastleSpawns != s.Map.CastleSpawns {
		t.Fatal("map geometry changed")
	}
	checkThreeLevelCatalog(t, s.Ruleset)
	m, err := game.NewMatch("levels", s, 100)
	if err != nil {
		t.Fatal(err)
	}
	purchases := 0
	for !m.Finished() {
		tr, err := m.Advance()
		if err != nil {
			t.Fatal(err)
		}
		purchases += countUpgradePurchases(tr.Events)
	}
	if purchases == 0 {
		t.Fatal("scripted controllers never bought upgrades")
	}
}

func countUpgradePurchases(events []game.Event) int {
	count := 0
	for _, event := range events {
		if event.Type == "upgrade_purchased" {
			count++
		}
	}
	return count
}

// Equal openings spend all starting gold, comparing a level-I upgrade with the
// extra troops its price buys. Both map orientations are run; results describe
// this probe, not general balance.
func TestFrontierUpgradeBalanceProbe(t *testing.T) {
	c, err := LoadCatalog(gamedata.Files)
	if err != nil {
		t.Fatal(err)
	}
	s, err := c.Resolve("frontier-siege", [2]service.FactionIdentity{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, track := range []string{"arsenal", "plating"} {
		for _, mirror := range []bool{false, true} {
			runUpgradeBalanceProbe(t, s, track, mirror)
		}
	}
}

func checkThreeLevelCatalog(t *testing.T, ruleset game.Ruleset) {
	t.Helper()
	for _, track := range []string{"arsenal", "plating", "industry"} {
		defs := game.UpgradesByID(ruleset)
		for i, id := range []string{track, track + "_2", track + "_3"} {
			if defs[id].Cost != []int64{160, 400, 800}[i] || defs[id].Level != i+1 {
				t.Fatalf("bad catalog entry %+v", defs[id])
			}
		}
	}
}

func runUpgradeBalanceProbe(t *testing.T, s game.Scenario, track string, mirror bool) {
	t.Helper()
	// This historical equal-budget probe intentionally measures batch purchases.
	s.Ruleset.ContinuousProduction = false
	upgraded := []controller.RecruitmentOrder{{UnitType: "vanguard", Count: 1}, {UnitType: "striker", Count: 1}}
	army := []controller.RecruitmentOrder{{UnitType: "vanguard", Count: 1}, {UnitType: "striker", Count: 1}, {UnitType: "crossbowman", Count: 4}}
	units := game.UnitTypesByID(s.Ruleset)
	budget := probeCost(units, upgraded) + game.UpgradesByID(s.Ruleset)[track].Cost
	if budget != probeCost(units, army) || budget != s.Ruleset.StartingGold {
		t.Fatal("unequal budgets")
	}
	plan := func(units []controller.RecruitmentOrder, upgrades []string) controller.PlanProposal {
		empty := []controller.ConstructionOrder{}
		return controller.PlanProposal{Name: "probe", RecruitmentOrders: &units, ConstructionOrders: &empty, UpgradeOrders: upgrades, SpendOrder: []string{"upgrades", "recruitment"}, ActionWeights: map[string]int64{"attack": 1000, "defend": 1}}
	}
	a, b := plan(upgraded, []string{track}), plan(army, nil)
	if mirror {
		a, b = b, a
	}
	m, err := game.NewMatch("probe", s, 200)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.AdvanceWithPlans(map[string]controller.PlanProposal{"a": a, "b": b}); err != nil {
		t.Fatal(err)
	}
	var last game.Transition
	for !m.Finished() {
		var plans map[string]controller.PlanProposal
		if m.DecisionWindowDue() {
			plans = map[string]controller.PlanProposal{"a": plan([]controller.RecruitmentOrder{}, nil), "b": plan([]controller.RecruitmentOrder{}, nil)}
		}
		last, err = m.AdvanceWithPlans(plans)
		if err != nil {
			t.Fatal(err)
		}
	}
	winner := "draw"
	if last.Result.WinnerFactionID != nil {
		winner = *last.Result.WinnerFactionID
	}
	t.Logf("track=%s mirrored=%v budget=%d winner=%s ticks=%d survivors=%d/%d", track, mirror, budget, winner, last.Tick, last.Result.Factions[0].UnitsRemaining, last.Result.Factions[1].UnitsRemaining)
}
