package catalog

import (
	"testing"

	"github.com/twhAuto/autonomous_battlefield/backend/gamedata"
	service "github.com/twhAuto/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/game"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/game/controller"
)

// These are reproducible balance probes, not promises that one army always wins.
// Mirror each equal-budget opening to expose side/turn-order bias.
func TestFrontierEqualBudgetMatchups(t *testing.T) {
	scenario := legacyScenario(t, "frontier-siege-v2")
	tests := []struct {
		name        string
		left, right []controller.RecruitmentOrder
	}{
		{"support", []controller.RecruitmentOrder{{UnitType: "vanguard", Count: 2}, {UnitType: "ranger", Count: 2}}, []controller.RecruitmentOrder{{UnitType: "vanguard", Count: 1}, {UnitType: "striker", Count: 1}, {UnitType: "ranger", Count: 1}, {UnitType: "herbalist", Count: 1}}},
		{"pikes-vs-mobile", []controller.RecruitmentOrder{{UnitType: "pikeman", Count: 5}}, []controller.RecruitmentOrder{{UnitType: "striker", Count: 6}}},
		{"siege-escort", []controller.RecruitmentOrder{{UnitType: "vanguard", Count: 2}, {UnitType: "catapult", Count: 1}}, []controller.RecruitmentOrder{{UnitType: "vanguard", Count: 4}}},
	}
	definitions := game.UnitTypesByID(scenario.Ruleset)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cost := func(orders []controller.RecruitmentOrder) int64 { return probeCost(definitions, orders) }
			if cost(tc.left) != cost(tc.right) {
				t.Fatal("probe budgets differ")
			}
			for _, mirrored := range []bool{false, true} {
				left, right := tc.left, tc.right
				if mirrored {
					left, right = right, left
				}
				runBalanceProbe(t, scenario, left, right, mirrored, cost(left))
			}
		})
	}
}

func probeCost(definitions map[string]game.UnitType, orders []controller.RecruitmentOrder) int64 {
	total := int64(0)
	for _, o := range orders {
		total += definitions[o.UnitType].Cost * int64(o.Count)
	}
	return total

}

func runBalanceProbe(t *testing.T, scenario game.Scenario, left, right []controller.RecruitmentOrder, mirrored bool, cost int64) {
	m, err := game.NewMatch("probe", scenario, 200)
	if err != nil {
		t.Fatal(err)
	}
	plan := func(orders []controller.RecruitmentOrder) controller.PlanProposal {
		empty := []controller.ConstructionOrder{}
		return controller.PlanProposal{Name: "probe", SpendOrder: []string{"recruitment"}, RecruitmentOrders: &orders, ConstructionOrders: &empty, ActionWeights: map[string]int64{"attack": 1000, "defend": 1}}
	}
	_, err = m.AdvanceWithPlans(map[string]controller.PlanProposal{"a": plan(left), "b": plan(right)})
	if err != nil {
		t.Fatal(err)
	}
	heals := 0
	var last game.Transition
	for !m.Finished() {
		var plans map[string]controller.PlanProposal
		if m.DecisionWindowDue() {
			plans = map[string]controller.PlanProposal{"a": plan([]controller.RecruitmentOrder{}), "b": plan([]controller.RecruitmentOrder{})}
		}
		last, err = m.AdvanceWithPlans(plans)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range last.Events {
			if e.Type == "unit_healed" {
				heals++
			}
		}
	}
	winner := "draw"
	if last.Result.WinnerFactionID != nil {
		winner = *last.Result.WinnerFactionID
	}
	t.Logf("budget=%d mirrored=%v winner=%s ticks=%d heals=%d survivors=%d/%d", cost, mirrored, winner, last.Tick, heals, last.Result.Factions[0].UnitsRemaining, last.Result.Factions[1].UnitsRemaining)
}

// The live ruleset's fully upgraded scripted armies must still break a castle
// before the tick limit, from either side, without exceeding the unit cap.
func TestLiveFrontierUpgradedBattleFinishes(t *testing.T) {
	skipSequentialBattleUnderRace(t)
	c, err := LoadCatalog(gamedata.Files)
	if err != nil {
		t.Fatal(err)
	}
	for _, identities := range [][2]service.FactionIdentity{{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}}, {{ID: "b", Name: "B"}, {ID: "a", Name: "A"}}} {
		scenario, err := c.Resolve("frontier-siege", identities)
		if err != nil {
			t.Fatal(err)
		}
		if scenario.Ruleset.MaximumUnitCount == 0 {
			t.Fatal("live ruleset has no unit cap")
		}
		last := runCappedProbe(t, scenario, identities)
		if last.Result.Reason != "castle_destroyed" {
			t.Fatalf("upgraded battle stalled until tick %d: %s", last.Tick, last.Result.Reason)
		}
		t.Logf("first=%s ended tick=%d", identities[0].ID, last.Tick)
	}
}

func runCappedProbe(t *testing.T, scenario game.Scenario, identities [2]service.FactionIdentity) game.Transition {
	t.Helper()
	m, err := game.NewMatch("live-probe", scenario, 400)
	if err != nil {
		t.Fatal(err)
	}
	var last game.Transition
	for !m.Finished() {
		if last, err = m.Advance(); err != nil {
			t.Fatal(err)
		}
		for _, identity := range identities {
			if count := m.UnitCount(identity.ID); count > scenario.Ruleset.MaximumUnitCount {
				t.Fatalf("faction %s holds %d units above the cap at tick %d", identity.ID, count, last.Tick)
			}
		}
	}
	return last
}
