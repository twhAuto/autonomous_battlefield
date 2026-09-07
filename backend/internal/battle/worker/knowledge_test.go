// Checks retrieved knowledge is frozen before the decision observation is hashed and
// persisted.

package worker

import (
	"context"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"testing"
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
	w, err := New(repo, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.resolveDecisionWindow(context.Background(), job.Lease, simulation); err != nil {
		t.Fatal(err)
	}
	for _, record := range repo.records {
		k := record.Observation.Knowledge
		if k == nil || k.Limits != cfg.KnowledgeRetrieval || len(k.Cards) != 1 {
			t.Fatal("missing knowledge")
		}
		h, err := ai.ObservationHash(record.Observation)
		if err != nil || h != record.Observation.ObservationHash {
			t.Fatal("knowledge not hashed")
		}
	}
}
