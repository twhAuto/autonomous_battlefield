// Checks retrieved knowledge is frozen before the decision observation is hashed and
// persisted.

package worker

import (
	"context"
	"testing"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai/decision"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/game"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/game/controller"
)

func TestKnowledgePersistedBeforeDecisionHash(t *testing.T) {
	job := testJob(20)
	for i := range job.Scenario.Factions {
		job.Scenario.Factions[i].Controller.Type = ai.ControllerScripted
	}
	job.Scenario.Ruleset.Knowledge = &game.RulesKnowledge{Mechanics: "mechanics", Cards: []game.UnitCard{{UnitID: "vanguard", Role: "role", Strengths: "strengths", Weaknesses: "weaknesses", Guidance: "guide"}}}
	simulation, err := game.NewMatch(job.Lease.BattleID, job.Scenario, 20)
	if err != nil {
		t.Fatal(err)
	}
	for range 9 {
		if _, err = simulation.Advance(); err != nil {
			t.Fatal(err)
		}
	}
	repo := &decisionBattleRepository{fakeBattleRepository: &fakeBattleRepository{}, records: map[string]ai.DecisionRecord{}}
	cfg := testWorkerConfig()
	cfg.KnowledgeRetrieval = ai.KnowledgeLimits{MaxUnitCards: 32, MaxBytes: 32768}
	w, err := New(repo, repo, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.resolveDecisionWindow(context.Background(), job.Lease, simulation); err != nil {
		t.Fatal(err)
	}
	for _, record := range repo.records {
		assertFrozenKnowledge(t, record, cfg.KnowledgeRetrieval)
	}
}

func assertFrozenKnowledge(t *testing.T, record ai.DecisionRecord, limits ai.KnowledgeLimits) {
	t.Helper()
	if record.Observation.SchemaVersion != ai.ContextSchema || record.Observation.UnitReference == nil {
		t.Fatal("compact reference not frozen")
	}
	k := record.Observation.Knowledge
	if k == nil || k.Limits != limits || len(k.Cards) != 1 {
		t.Fatal("missing knowledge")
	}
	h, err := decision.ObservationHash(record.Observation)
	if err != nil || h != record.Observation.ObservationHash {
		t.Fatal("knowledge not hashed")
	}
}

// A full building limit offers no legal site, so scripted plans must not order
// construction that the frozen validator would reject and stop the window.
func TestScriptedWindowAtBuildingLimitIsAccepted(t *testing.T) {
	job := testJob(40)
	for i := range job.Scenario.Factions {
		job.Scenario.Factions[i].Controller.Type = ai.ControllerScripted
	}
	// Sturdier castles keep the fixture's battle running past its first window.
	job.Scenario.Ruleset.Buildings.MaximumBuildingCount, job.Scenario.Ruleset.Buildings.CastleHealth = 1, 1000
	simulation, err := game.NewMatch(job.Lease.BattleID, job.Scenario, 40)
	if err != nil {
		t.Fatal(err)
	}
	fillBuildingLimit(t, simulation, job.Scenario.Factions[0].ID)
	repo := &decisionBattleRepository{fakeBattleRepository: &fakeBattleRepository{}, records: map[string]ai.DecisionRecord{}}
	w, err := New(repo, repo, testWorkerConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.resolveDecisionWindow(context.Background(), job.Lease, simulation); err != nil {
		t.Fatal("scripted window failed at the building limit:", err)
	}
	for _, record := range repo.records {
		if record.FactionID == job.Scenario.Factions[0].ID && (record.Status != "accepted" || len(record.Observation.Legal.EconomySites) != 0) {
			t.Fatalf("decision at the limit: status %s, %d legal sites", record.Status, len(record.Observation.Legal.EconomySites))
		}
	}
}

// fillBuildingLimit builds one economy building on the faction's first pad, then
// advances to the next decision window.
func fillBuildingLimit(t *testing.T, simulation *game.Match, factionID string) {
	t.Helper()
	pad := simulation.Scenario().Map.EconomySites[0][0]
	build := []controller.ConstructionOrder{{BuildingType: game.BuildingEconomy, Position: controller.Coordinate{X: pad.X, Y: pad.Y}}}
	plan := controller.PlanProposal{Name: "build", SpendOrder: []string{"construction"}, ConstructionOrders: &build, ActionWeights: map[string]int64{game.ActionDefend: 1000}}
	for simulation.LivingStructureCount(factionID) == 0 || !simulation.DecisionWindowDue() {
		if simulation.Finished() {
			t.Fatal("faction never filled its building limit")
		}
		var plans map[string]controller.PlanProposal
		if simulation.DecisionWindowDue() && !simulation.OpeningDue() && simulation.LivingStructureCount(factionID) == 0 {
			plans = map[string]controller.PlanProposal{factionID: plan}
		}
		if _, err := simulation.AdvanceWithPlans(plans); err != nil {
			t.Fatal(err)
		}
	}
}
