package game

import (
	"reflect"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/battle/simulation/controller"
)

func TestOddRowHexNeighborsAndDistance(t *testing.T) {
	even := Neighbors(Position{X: 3, Y: 2})
	odd := Neighbors(Position{X: 3, Y: 3})
	if len(even) != 6 || len(odd) != 6 || reflect.DeepEqual(even, odd) {
		t.Fatalf("unexpected neighbors: even=%v odd=%v", even, odd)
	}
	for _, neighbor := range even {
		if HexDistance(Position{X: 3, Y: 2}, neighbor) != 1 {
			t.Fatalf("neighbor %v is not one hex away", neighbor)
		}
	}
	if got := HexDistance(Position{X: 1, Y: 5}, Position{X: 13, Y: 5}); got != 12 {
		t.Fatalf("castle distance = %d, want 12", got)
	}
}

func TestAuthoredMapValidation(t *testing.T) {
	valid := testMap()
	if err := ValidateMap(valid); err != nil {
		t.Fatalf("ValidateMap() = %v", err)
	}
	for _, mutate := range []func(*Map){
		func(value *Map) { value.Kind = "seeded" },
		func(value *Map) { value.Rows[1] = "......##......." },
		func(value *Map) { value.CastleSpawns[1] = Position{X: 12, Y: 5} },
		func(value *Map) { value.EconomySites[1][0] = value.EconomySites[0][0] },
	} {
		candidate := CloneMap(valid)
		mutate(&candidate)
		if err := ValidateMap(candidate); err == nil {
			t.Fatalf("invalid map accepted: %#v", candidate)
		}
	}
}

func TestBlueprintBudgetPriorityAndUpgrades(t *testing.T) {
	ruleset := testRuleset()
	blueprint := defaultBlueprint()
	cost, err := ValidateBlueprint(ruleset, blueprint)
	if err != nil || cost != 110 {
		t.Fatalf("ValidateBlueprint() = %d, %v; want 110", cost, err)
	}
	attack, defense, economy := upgradeBonuses(ruleset, blueprint)
	if attack != 2 || defense != 0 || economy != 0 {
		t.Fatalf("bonuses = %d/%d/%d", attack, defense, economy)
	}
	invalid := CloneBlueprint(blueprint)
	invalid.UpgradeIDs = append(invalid.UpgradeIDs, "plating")
	if _, err := ValidateBlueprint(ruleset, invalid); err == nil {
		t.Fatal("over-budget blueprint was accepted")
	}
	invalid = CloneBlueprint(blueprint)
	invalid.RecruitmentPriority = []string{"vanguard", "striker", "striker"}
	if _, err := ValidateBlueprint(ruleset, invalid); err == nil {
		t.Fatal("duplicate recruitment priority was accepted")
	}
}

func TestMatchIsDeterministicAndPreservesInvariants(t *testing.T) {
	first, err := NewMatch("battle", testScenario(), 160)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewMatch("battle", testScenario(), 160)
	if err != nil {
		t.Fatal(err)
	}
	sawMovementDirection := false
	sawAttackTarget := false
	for !first.Finished() {
		left := advanceMatchingBattles(t, first, second)
		assertStateInvariants(t, left.State)
		moved, attacked := stateHasActionMetadata(left.State)
		sawMovementDirection = sawMovementDirection || moved
		sawAttackTarget = sawAttackTarget || attacked
	}
	if !sawMovementDirection || !sawAttackTarget {
		t.Fatalf("action metadata movement=%v attack=%v", sawMovementDirection, sawAttackTarget)
	}
}

func advanceMatchingBattles(t *testing.T, first, second *Match) Transition {
	t.Helper()
	left, err := first.Advance()
	if err != nil {
		t.Fatal(err)
	}
	right, err := second.Advance()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(left, right) {
		t.Fatalf("transitions differ at tick %d", left.Tick)
	}
	return left
}

func stateHasActionMetadata(state State) (bool, bool) {
	moved := false
	attacked := false
	for _, unit := range state.Units {
		moved = moved || unit.MovedFrom != nil
		attacked = attacked || (unit.AttackTargetID != "" && unit.AttackTargetPosition != nil)
	}
	return moved, attacked
}

func TestEconomyActivationAndDraw(t *testing.T) {
	match, err := NewMatch("economy", testScenario(), 1)
	if err != nil {
		t.Fatal(err)
	}
	transition, err := match.Advance()
	if err != nil {
		t.Fatal(err)
	}
	if !transition.Finished || transition.Result == nil || transition.Result.Reason != "tick_limit" {
		t.Fatalf("transition = %#v", transition)
	}
	for _, faction := range transition.State.Factions {
		if faction.Gold != 70 || faction.GoldEarned != 10 || faction.BuildingsBuilt != 0 {
			t.Fatalf("economy state = %#v", faction)
		}
	}
	wantOrder := []string{"battle_started", "income_collected"}
	for index, kind := range wantOrder {
		if transition.Events[index].Type != kind {
			t.Fatalf("event %d = %q, want %q", index, transition.Events[index].Type, kind)
		}
	}
}

func TestProductionOccursOnlyAtDecisionWindows(t *testing.T) {
	match, err := NewMatch("windows", testScenario(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if match.TickDuration() != testScenario().TickDuration || match.State().Tick != 0 || match.Scenario().ID != testScenario().ID || len(match.FactionIDs()) != 2 {
		t.Fatal("match metadata accessors returned unexpected values")
	}
	for tick := 1; tick < 10; tick++ {
		if match.DecisionWindowDue() {
			t.Fatalf("decision due before tick 10 at %d", tick)
		}
		transition, err := match.Advance()
		if err != nil {
			t.Fatal(err)
		}
		for _, faction := range transition.State.Factions {
			if faction.BuildingsBuilt != 0 || faction.UnitsRecruited != 0 {
				t.Fatalf("production at tick %d: %#v", tick, faction)
			}
		}
	}
	if !match.DecisionWindowDue() || match.DecisionTargetTick() != 10 {
		t.Fatal("tick 10 decision was not due")
	}
	snapshot, err := match.DecisionSnapshot("faction-a")
	if err != nil || snapshot.TargetTick != 10 || snapshot.Version != 9 || snapshot.FactionID != "faction-a" {
		t.Fatalf("snapshot=%#v err=%v", snapshot, err)
	}
	transition, err := match.Advance()
	if err != nil {
		t.Fatal(err)
	}
	for _, faction := range transition.State.Factions {
		if faction.BuildingsBuilt != 2 || faction.UnitsRecruited < 1 {
			t.Fatalf("window production=%#v", faction)
		}
	}
}

func TestProductionReservePreventsOverspending(t *testing.T) {
	match, err := NewMatch("reserve", testScenario(), 10)
	if err != nil {
		t.Fatal(err)
	}
	for range 9 {
		_, _ = match.Advance()
	}
	plans := match.ScriptedPlans()
	for id, plan := range plans {
		plan.ReserveGold = 1000
		plans[id] = plan
	}
	transition, err := match.AdvanceWithPlans(plans)
	if err != nil {
		t.Fatal(err)
	}
	for _, faction := range transition.State.Factions {
		if faction.BuildingsBuilt != 0 || faction.UnitsRecruited != 0 || faction.GoldSpent != 0 {
			t.Fatalf("reserve state=%#v", faction)
		}
	}
}

func TestFactionViewHidesEnemiesOutsideVisionButKeepsCastle(t *testing.T) {
	match, err := NewMatch("view", testScenario(), 10)
	if err != nil {
		t.Fatal(err)
	}
	view, err := match.View("faction-a")
	if err != nil {
		t.Fatal(err)
	}
	enemyUnits := 0
	enemyCastle := false
	for _, unit := range view.State.Units {
		if unit.FactionID == "faction-b" {
			enemyUnits++
		}
	}
	for _, building := range view.State.Buildings {
		if building.FactionID == "faction-b" && building.Type == BuildingCastle {
			enemyCastle = true
		}
	}
	if enemyUnits != 0 || !enemyCastle {
		t.Fatalf("view exposes %d enemy units, castle=%v", enemyUnits, enemyCastle)
	}
	if _, err := match.View("outsider"); err == nil {
		t.Fatal("outsider view was accepted")
	}
}

func TestCompleteLocalMatchReachesTerminalResult(t *testing.T) {
	match, err := NewMatch("complete", testScenario(), 1000)
	if err != nil {
		t.Fatal(err)
	}
	var final Transition
	for !match.Finished() {
		final, err = match.Advance()
		if err != nil {
			t.Fatal(err)
		}
	}
	if final.Result == nil || (final.Result.Reason != "castle_destroyed" && final.Result.Reason != "tick_limit") {
		t.Fatalf("full match result = %#v", final.Result)
	}
	for _, unit := range final.State.Units {
		if unit.Health <= 0 {
			t.Fatalf("destroyed unit retained in final state: %#v", unit)
		}
	}
}

func assertStateInvariants(t *testing.T, state State) {
	t.Helper()
	occupied := map[Position]string{}
	owners := map[string]bool{}
	for _, faction := range state.Factions {
		owners[faction.FactionID] = true
		if faction.Gold < 0 {
			t.Fatalf("negative gold: %#v", faction)
		}
	}
	for _, unit := range state.Units {
		if unit.Health <= 0 {
			t.Fatalf("destroyed unit retained in state: %#v", unit)
		}
		if !owners[unit.FactionID] {
			t.Fatalf("unknown owner: %#v", unit)
		}
		if previous := occupied[unit.Position]; previous != "" {
			t.Fatalf("collision: %s and %s", previous, unit.ID)
		}
		occupied[unit.Position] = unit.ID
	}
	for _, building := range state.Buildings {
		if building.Health <= 0 {
			continue
		}
		if previous := occupied[building.Position]; previous != "" {
			t.Fatalf("collision: %s and %s", previous, building.ID)
		}
		occupied[building.Position] = building.ID
	}
}

func testScenario() Scenario {
	plan := controller.PlanProposal{Name: "attack", ActionWeights: map[string]int64{ActionAttack: 1500, ActionDefend: 500}}
	return Scenario{ID: "castle-siege-v1", RulesVersion: "1", RulesetID: "castle-siege-v1", DecisionIntervalTicks: 10, DecisionLeadTicks: 2, Map: testMap(), Ruleset: testRuleset(), Factions: []FactionSetup{{ID: "faction-a", Name: "A", Blueprint: defaultBlueprint(), FallbackPlan: plan, ScriptedPlans: []controller.PlanProposal{plan}}, {ID: "faction-b", Name: "B", Blueprint: defaultBlueprint(), FallbackPlan: plan, ScriptedPlans: []controller.PlanProposal{plan}}}}
}

func defaultBlueprint() Blueprint {
	return Blueprint{Units: []UnitSelection{{Type: "vanguard", Count: 1}, {Type: "striker", Count: 1}, {Type: "ranger", Count: 1}}, UpgradeIDs: []string{"arsenal"}, RecruitmentPriority: []string{"vanguard", "striker", "ranger"}}
}
func testMap() Map {
	return Map{Kind: "authored", Width: 15, Height: 11, Rows: []string{"...............", ".......#.......", "..b....#....b..", "...............", ".......#.......", "...............", ".......#.......", "...............", "..b....#....b..", ".......#.......", "..............."}, CastleSpawns: [2]Position{{X: 1, Y: 5}, {X: 13, Y: 5}}, EconomySites: [2][]Position{{{X: 2, Y: 2}, {X: 2, Y: 8}}, {{X: 12, Y: 2}, {X: 12, Y: 8}}}}
}
func testRuleset() Ruleset {
	return Ruleset{ID: "castle-siege-v1", Version: "1", DesignBudget: 120, StartingGold: 60, Units: []UnitType{{ID: "vanguard", Cost: 35, Health: 70, Attack: 9, Defense: 7, Speed: 4, Movement: 1, Range: 1, Vision: 2}, {ID: "striker", Cost: 25, Health: 42, Attack: 14, Defense: 3, Speed: 8, Movement: 2, Range: 1, Vision: 3}, {ID: "ranger", Cost: 30, Health: 30, Attack: 11, Defense: 2, Speed: 6, Movement: 1, Range: 3, Vision: 4}}, Upgrades: []Upgrade{{ID: "arsenal", Cost: 20, AttackBonus: 2}, {ID: "plating", Cost: 20, DefenseBonus: 2}, {ID: "industry", Cost: 20, EconomyIncomeBonus: 2}}, Buildings: BuildingRules{CastleHealth: 300, CastleDefense: 8, CastleVision: 4, CastleIncome: 10, EconomyCost: 50, EconomyHealth: 60, EconomyDefense: 2, EconomyVision: 2, EconomyIncome: 10, MaximumEconomyCount: 2}, Combat: CombatRules{MinimumDamage: 1, DefendAttackPercent: 80, DefendDamageTakenPercent: 70, LootPercent: 50}}
}
