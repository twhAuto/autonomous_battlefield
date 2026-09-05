package ai

import (
	"encoding/json"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
	"strings"
	"testing"
)

func memoryRecord(tick uint64) DecisionRecord {
	return DecisionRecord{BattleID: "battle", Attempt: 2, FactionID: "blue", TargetTick: tick, Status: "accepted", ResolvedPlan: &controller.PlanProposal{Name: "reserve", ReserveGold: 100}, Observation: DecisionContext{Economy: EconomyContext{Gold: int64(tick)}, View: game.View{State: game.State{Tick: tick - 1, Units: []game.Unit{{FactionID: "blue"}, {FactionID: "red"}}}}}}
}
func TestFactionMemoryScopeChronologyAndBounds(t *testing.T) {
	records := []DecisionRecord{memoryRecord(10), memoryRecord(20), memoryRecord(30), memoryRecord(40)}
	for _, change := range []func(*DecisionRecord){func(r *DecisionRecord) { r.BattleID = "other" }, func(r *DecisionRecord) { r.Attempt = 1 }, func(r *DecisionRecord) { r.FactionID = "red" }, func(r *DecisionRecord) { r.Status = "running" }, func(r *DecisionRecord) { r.ResolvedPlan = nil }} {
		bad := memoryRecord(45)
		change(&bad)
		records = append(records, bad)
	}
	records = append(records, memoryRecord(50), memoryRecord(60))
	records[2].Status = "fallback"
	got := BuildFactionMemory(records, "battle", 2, "blue", 50)
	if len(got) != 3 || got[0].TargetTick != 20 || got[2].TargetTick != 40 || got[1].Status != "fallback" || got[0].ObservationTick != 19 || got[0].Economy.Gold != 20 || got[0].OwnUnits != 1 || got[0].VisibleEnemyUnits != 1 {
		t.Fatalf("memory=%+v", got)
	}
	oversized := memoryRecord(45)
	oversized.ResolvedPlan.Rationale = strings.Repeat("x", FactionMemoryMaxBytes)
	got = BuildFactionMemory(append(records, oversized), "battle", 2, "blue", 50)
	payload, _ := json.Marshal(got)
	if len(payload) > FactionMemoryMaxBytes || strings.Contains(string(payload), strings.Repeat("x", 20)) {
		t.Fatal("oversized memory escaped limit")
	}
	if len(BuildFactionMemory(records, "other", 3, "red", 1)) != 0 {
		t.Fatal("scope leak")
	}
}
func TestObservationHashIncludesMemory(t *testing.T) {
	c := DecisionContext{RequestID: "one"}
	before, _ := ObservationHash(c)
	c.Memory = BuildFactionMemory([]DecisionRecord{memoryRecord(10)}, "battle", 2, "blue", 20)
	after, _ := ObservationHash(c)
	if before == after {
		t.Fatal("memory changes must invalidate reuse")
	}
	c.RequestID = "two"
	equivalent, _ := ObservationHash(c)
	if after != equivalent {
		t.Fatal("request id must not affect observation hash")
	}
}
