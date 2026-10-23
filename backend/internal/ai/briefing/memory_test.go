// Checks faction-memory scope, chronological ordering, size bounds, and contribution to
// observation hashes.

package briefing

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/decision"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
)

func memoryRecord(tick uint64) ai.DecisionRecord {
	return ai.DecisionRecord{BattleID: "battle", Attempt: 2, FactionID: "blue", TargetTick: tick, Status: "accepted", ResolvedPlan: &controller.PlanProposal{Name: "reserve", ReserveGold: 100}, Observation: ai.DecisionContext{Economy: ai.EconomyContext{Gold: int64(tick)}, View: game.View{State: game.State{Tick: tick - 1, Units: []game.Unit{{FactionID: "blue"}, {FactionID: "red"}}}}}}
}
func TestFactionMemoryScopeChronologyAndBounds(t *testing.T) {
	records := []ai.DecisionRecord{memoryRecord(10), memoryRecord(20), memoryRecord(30), memoryRecord(40)}
	for _, change := range []func(*ai.DecisionRecord){func(r *ai.DecisionRecord) { r.BattleID = "other" }, func(r *ai.DecisionRecord) { r.Attempt = 1 }, func(r *ai.DecisionRecord) { r.FactionID = "red" }, func(r *ai.DecisionRecord) { r.Status = "running" }, func(r *ai.DecisionRecord) { r.ResolvedPlan = nil }} {
		bad := memoryRecord(45)
		change(&bad)
		records = append(records, bad)
	}
	records = append(records, memoryRecord(50), memoryRecord(60))
	records[2].Status = "fallback"
	got := BuildFactionMemory(records, nil, "battle", 2, "blue", 50)
	if len(got) != 3 || got[0].TargetTick != 20 || got[2].TargetTick != 40 || got[1].Status != "fallback" || got[0].ObservationTick != 19 || got[0].Economy.Gold != 20 || got[0].OwnUnits != 1 || got[0].VisibleEnemyUnits != 1 {
		t.Fatalf("memory=%+v", got)
	}
	oversized := memoryRecord(45)
	oversized.ResolvedPlan.Rationale = strings.Repeat("x", FactionMemoryMaxBytes)
	got = BuildFactionMemory(append(records, oversized), nil, "battle", 2, "blue", 50)
	payload, _ := json.Marshal(got)
	if len(payload) > FactionMemoryMaxBytes || strings.Contains(string(payload), strings.Repeat("x", 20)) {
		t.Fatal("oversized memory escaped limit")
	}
	if len(BuildFactionMemory(records, nil, "other", 3, "red", 1)) != 0 {
		t.Fatal("scope leak")
	}
}
func TestObservationHashIncludesMemory(t *testing.T) {
	c := ai.DecisionContext{RequestID: "one"}
	before, _ := decision.ObservationHash(c)
	c.Memory = BuildFactionMemory([]ai.DecisionRecord{memoryRecord(10)}, nil, "battle", 2, "blue", 20)
	after, _ := decision.ObservationHash(c)
	if before == after {
		t.Fatal("memory changes must invalidate reuse")
	}
	c.RequestID = "two"
	equivalent, _ := decision.ObservationHash(c)
	if after != equivalent {
		t.Fatal("request id must not affect observation hash")
	}
}

func TestFactionMemoryReportsPlanOutcomesPerWindow(t *testing.T) {
	events := []OutcomeEvent{
		{Tick: 9, Type: "unit_destroyed", FactionID: "blue"},
		{Tick: 10, Type: "unit_recruited", FactionID: "blue"},
		{Tick: 12, Type: "unit_destroyed", FactionID: "blue"},
		{Tick: 13, Type: "unit_destroyed", FactionID: "red"},
		{Tick: 15, Type: "production_skipped", FactionID: "blue", Kind: "construction", Reason: "maximum_reached"},
		{Tick: 16, Type: "production_skipped", FactionID: "blue", Kind: "construction", Reason: "maximum_reached"},
		{Tick: 17, Type: "production_skipped", FactionID: "red", Kind: "recruitment", Reason: "unit_limit"},
		{Tick: 19, Type: "building_destroyed", FactionID: "red"},
		{Tick: 20, Type: "building_constructed", FactionID: "blue"},
		{Tick: 25, Type: "building_destroyed", FactionID: "blue"},
		{Tick: 30, Type: "unit_recruited", FactionID: "blue"},
	}
	got := BuildFactionMemory([]ai.DecisionRecord{memoryRecord(10), memoryRecord(20)}, events, "battle", 2, "blue", 30)
	first := ai.PlanOutcome{ThroughTick: 19, UnitsTrained: 1, UnitsLost: 1, EnemyUnitsDestroyed: 1, EnemyBuildingsDestroyed: 1, Skipped: []ai.SkippedOrders{{Kind: "construction", Reason: "maximum_reached", Count: 2}}}
	second := ai.PlanOutcome{ThroughTick: 29, BuildingsConstructed: 1, BuildingsLost: 1}
	if len(got) != 2 || !reflect.DeepEqual(*got[0].Outcome, first) || !reflect.DeepEqual(*got[1].Outcome, second) {
		t.Fatalf("outcomes = %+v / %+v", got[0].Outcome, got[1].Outcome)
	}
}
