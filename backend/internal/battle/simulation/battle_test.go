package simulation

import (
	"reflect"
	"testing"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/battle/simulation/controller"
)

func TestBattleUsesInitiativeAndSkipsDestroyedUnits(t *testing.T) {
	scenario := testScenario()
	scenario.Factions[0].Units = []UnitTemplate{{ID: "fast", Health: 10, Attack: 20, Speed: 10}}
	scenario.Factions[1].Units = []UnitTemplate{{ID: "slow", Health: 5, Attack: 20, Speed: 1}}
	battle, err := NewBattle("battle-1", scenario, 10)
	if err != nil {
		t.Fatalf("NewBattle() returned an error: %v", err)
	}

	transition, err := battle.Advance()
	if err != nil {
		t.Fatalf("Advance() returned an error: %v", err)
	}
	if !transition.Finished || transition.Result == nil || transition.Result.WinnerFactionID == nil || *transition.Result.WinnerFactionID != "faction-a" {
		t.Fatalf("result = %#v, want faction-a elimination win", transition.Result)
	}
	if got := countEvents(transition.Events, "unit_attacked"); got != 1 {
		t.Fatalf("attack event count = %d, want 1", got)
	}
}

func TestBattleAppliesDefendAttackAndDamageModifiers(t *testing.T) {
	scenario := testScenario()
	scenario.Factions[0].FallbackPlan = controller.PlanProposal{Name: "defend", ActionWeights: map[string]int64{ActionAttack: 0, ActionDefend: 2000}}
	scenario.Factions[1].FallbackPlan = controller.PlanProposal{Name: "attack", ActionWeights: map[string]int64{ActionAttack: 2000, ActionDefend: 0}}
	scenario.Factions[0].Units = []UnitTemplate{{ID: "a", Health: 100, Attack: 20, Speed: 10}}
	scenario.Factions[1].Units = []UnitTemplate{{ID: "b", Health: 100, Attack: 20, Speed: 5}}
	battle, err := NewBattle("battle-1", scenario, 1)
	if err != nil {
		t.Fatalf("NewBattle() returned an error: %v", err)
	}

	transition, err := battle.Advance()
	if err != nil {
		t.Fatalf("Advance() returned an error: %v", err)
	}
	state := battle.State()
	wantHealth := map[string]int64{"faction-a:a": 90, "faction-b:b": 90}
	for _, unit := range state.Units {
		if unit.Health != wantHealth[unit.ID] {
			t.Fatalf("unit %s health = %d, want %d", unit.ID, unit.Health, wantHealth[unit.ID])
		}
	}
	if transition.Result == nil || transition.Result.Reason != "tick_limit" {
		t.Fatalf("result = %#v, want tick-limit draw", transition.Result)
	}
}

func TestBattleTargetsLowestHealthThenUnitID(t *testing.T) {
	scenario := testScenario()
	scenario.Factions[0].Units = []UnitTemplate{{ID: "attacker", Health: 100, Attack: 5, Speed: 10}}
	scenario.Factions[1].Units = []UnitTemplate{
		{ID: "zeta", Health: 20, Attack: 0, Speed: 0},
		{ID: "alpha", Health: 20, Attack: 0, Speed: 0},
	}
	battle, err := NewBattle("battle-1", scenario, 1)
	if err != nil {
		t.Fatalf("NewBattle() returned an error: %v", err)
	}
	transition, err := battle.Advance()
	if err != nil {
		t.Fatalf("Advance() returned an error: %v", err)
	}
	for _, event := range transition.Events {
		if event.Type == "unit_attacked" && event.Data["attackerId"] == "faction-a:attacker" {
			if event.Data["targetId"] != "faction-b:alpha" {
				t.Fatalf("target = %v, want faction-b:alpha", event.Data["targetId"])
			}
			return
		}
	}
	t.Fatal("attacker event was not emitted")
}

func TestBattleRepeatsFinalScriptedPlan(t *testing.T) {
	scenario := testScenario()
	scenario.DecisionIntervalTicks = 3
	scenario.DecisionLeadTicks = 1
	for factionIndex := range scenario.Factions {
		scenario.Factions[factionIndex].Units[0].Health = 1_000
		scenario.Factions[factionIndex].Units[0].Attack = 0
		scenario.Factions[factionIndex].ScriptedPlans = []controller.PlanProposal{
			{Name: "first", ActionWeights: map[string]int64{ActionAttack: 1000}},
			{Name: "final", ActionWeights: map[string]int64{ActionDefend: 1000}},
		}
	}
	battle, err := NewBattle("battle-1", scenario, 8)
	if err != nil {
		t.Fatalf("NewBattle() returned an error: %v", err)
	}
	namesByFaction := map[string][]string{}
	for !battle.Finished() {
		transition, err := battle.Advance()
		if err != nil {
			t.Fatalf("Advance() returned an error: %v", err)
		}
		for _, event := range transition.Events {
			if event.Type == "plan_accepted" {
				factionID, _ := event.Data["factionId"].(string)
				name, _ := event.Data["name"].(string)
				namesByFaction[factionID] = append(namesByFaction[factionID], name)
			}
		}
	}
	for factionID, names := range namesByFaction {
		want := []string{"first", "final", "final"}
		if !reflect.DeepEqual(names, want) {
			t.Fatalf("faction %s plans = %v, want %v", factionID, names, want)
		}
	}
	if len(namesByFaction) != 2 {
		t.Fatalf("plan history has %d factions, want 2", len(namesByFaction))
	}
}

func TestBattleIsDeterministic(t *testing.T) {
	first, err := NewBattle("battle-1", testScenario(), 20)
	if err != nil {
		t.Fatalf("NewBattle(first) returned an error: %v", err)
	}
	second, err := NewBattle("battle-1", testScenario(), 20)
	if err != nil {
		t.Fatalf("NewBattle(second) returned an error: %v", err)
	}

	var firstHistory, secondHistory []Event
	for !first.Finished() {
		transition, err := first.Advance()
		if err != nil {
			t.Fatalf("first Advance() returned an error: %v", err)
		}
		firstHistory = append(firstHistory, transition.Events...)
	}
	for !second.Finished() {
		transition, err := second.Advance()
		if err != nil {
			t.Fatalf("second Advance() returned an error: %v", err)
		}
		secondHistory = append(secondHistory, transition.Events...)
	}
	firstResult, _ := first.Result()
	secondResult, _ := second.Result()
	if !reflect.DeepEqual(firstHistory, secondHistory) || !reflect.DeepEqual(firstResult, secondResult) {
		t.Fatalf("replay differs\nfirst result: %#v\nsecond result: %#v", firstResult, secondResult)
	}
}

func TestBattleSummaryStatistics(t *testing.T) {
	scenario := testScenario()
	scenario.TickDuration = 250 * time.Millisecond
	battle, err := NewBattle("battle-1", scenario, 1)
	if err != nil {
		t.Fatalf("NewBattle() returned an error: %v", err)
	}
	if battle.TickDuration() != scenario.TickDuration {
		t.Fatalf("TickDuration() = %v, want %v", battle.TickDuration(), scenario.TickDuration)
	}
	transition, err := battle.Advance()
	if err != nil {
		t.Fatalf("Advance() returned an error: %v", err)
	}
	if transition.Result == nil || len(transition.Result.Factions) != 2 {
		t.Fatalf("result = %#v", transition.Result)
	}
	for _, stats := range transition.Result.Factions {
		if stats.UnitsStarted != 1 || stats.UnitsRemaining != 1 || stats.DamageDealt != 8 || stats.DamageTaken != 8 || stats.AttackTicks != 1 || stats.DefendTicks != 0 {
			t.Fatalf("stats = %#v", stats)
		}
	}
}

func TestBattleRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Scenario)
	}{
		{name: "zero health", mutate: func(s *Scenario) { s.Factions[0].Units[0].Health = 0 }},
		{name: "negative attack", mutate: func(s *Scenario) { s.Factions[0].Units[0].Attack = -1 }},
		{name: "oversized stat", mutate: func(s *Scenario) { s.Factions[0].Units[0].Speed = maxUnitStat + 1 }},
		{name: "invalid scripted plan", mutate: func(s *Scenario) {
			s.Factions[0].ScriptedPlans[0].ActionWeights["retreat"] = 1000
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scenario := testScenario()
			tt.mutate(&scenario)
			if _, err := NewBattle("battle-1", scenario, 10); err == nil {
				t.Fatal("NewBattle() returned no error")
			}
		})
	}
}

func testScenario() Scenario {
	attackPlan := controller.PlanProposal{Name: "attack", ActionWeights: map[string]int64{ActionAttack: 1500, ActionDefend: 500}}
	return Scenario{
		ID: "test", RulesVersion: "1", DecisionIntervalTicks: 10, DecisionLeadTicks: 2,
		Rules: CombatRules{MinimumDamage: 1, DefendAttackPercent: 50, DefendDamageTakenPercent: 50},
		Factions: []FactionSetup{
			{ID: "faction-a", Name: "A", Units: []UnitTemplate{{ID: "one", Health: 40, Attack: 10, Defense: 2, Speed: 8}}, FallbackPlan: attackPlan, ScriptedPlans: []controller.PlanProposal{attackPlan}},
			{ID: "faction-b", Name: "B", Units: []UnitTemplate{{ID: "one", Health: 40, Attack: 10, Defense: 2, Speed: 7}}, FallbackPlan: attackPlan, ScriptedPlans: []controller.PlanProposal{attackPlan}},
		},
	}
}

func countEvents(events []Event, eventType string) int {
	count := 0
	for _, event := range events {
		if event.Type == eventType {
			count++
		}
	}
	return count
}
