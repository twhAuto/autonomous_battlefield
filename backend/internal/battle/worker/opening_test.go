package worker

import (
	"context"
	"encoding/json"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"strings"
	"testing"
	"time"
)

func TestOpeningIsRecordedAndPersistedBeforeCombat(t *testing.T) {
	for _, model := range []bool{false, true} {
		job := testJob(2)
		job.Scenario.SnapshotVersion = game.DoctrineSnapshotVersion
		job.Scenario.Ruleset.StartingGold = 180
		for i := range job.Scenario.Factions {
			job.Scenario.Factions[i].Doctrine = []string{"Scout", "Defend"}[i]
			job.Scenario.Factions[i].Background = []string{"Northern ancestors", "Southern ancestors"}[i]
			job.Scenario.Factions[i].Controller = game.ControllerConfig{Type: ai.ControllerScripted}
		}
		if model {
			job.Scenario.Factions[0].Controller = game.ControllerConfig{Type: ai.ControllerModel, ProfileID: "test"}
		}
		repo := &decisionBattleRepository{fakeBattleRepository: &fakeBattleRepository{}, records: map[string]ai.DecisionRecord{}}
		config := testWorkerConfig()
		config.DecisionTimeout = time.Millisecond
		worker, _ := New(repo, config)
		simulation, err := game.NewMatch(job.Lease.BattleID, job.Scenario, job.RequestedTicks)
		if err != nil {
			t.Fatal(err)
		}
		_, finished, err := worker.runTick(context.Background(), job.Lease, simulation)
		if err != nil || finished {
			t.Fatalf("opening %v %v", finished, err)
		}
		if len(repo.saved) != 1 || repo.saved[0].Tick != 0 || !repo.saved[0].State.OpeningComplete {
			t.Fatal("opening not durable before tick one")
		}
		if len(repo.records) != 2 {
			t.Fatal("missing simultaneous decisions")
		}
		for _, record := range repo.records {
			if record.TargetTick != 0 || !record.Observation.Opening || len(record.Observation.View.State.Units) != 0 || record.ResolvedPlan == nil {
				t.Fatal("opening decisions not frozen")
			}
			expected := "Defend"
			if record.FactionID == job.Scenario.Factions[0].ID {
				expected = "Scout"
			}
			owner, opponent := job.Scenario.Factions[1], job.Scenario.Factions[0]
			if record.FactionID == job.Scenario.Factions[0].ID {
				owner, opponent = opponent, owner
			}
			raw, _ := json.Marshal(record.Observation)
			if record.Observation.Background != owner.Background || record.Observation.FactionName != owner.Name || strings.Contains(string(raw), opponent.Background) {
				t.Fatal("identity isolation", string(raw))
			}
			if record.Observation.Doctrine != expected {
				t.Fatal("wrong doctrine exposed")
			}
			if record.ControllerType == ai.ControllerModel && (record.Status != "fallback" || record.FallbackSource != "scripted_fallback") {
				t.Fatal("opening timeout fallback")
			}
		}
		_, _, err = worker.runTick(context.Background(), job.Lease, simulation)
		if err != nil {
			t.Fatal(err)
		}
		if simulation.State().Tick != 1 || len(repo.records) != 2 {
			t.Fatal("opening repeated")
		}
	}
}

func TestDoctrineBattleCompletesWithAcceptedModelStub(t *testing.T) {
	job := testJob(20)
	job.Scenario.SnapshotVersion = game.DoctrineSnapshotVersion
	job.Scenario.Ruleset.StartingGold = 180
	job.Scenario.Ruleset.Buildings.CastleHealth = 100000
	for i := range job.Scenario.Factions {
		job.Scenario.Factions[i].Controller = game.ControllerConfig{Type: ai.ControllerModel, ProfileID: "stub"}
		job.Scenario.Factions[i].Doctrine = "Buy protection"
	}
	repo := &decisionBattleRepository{fakeBattleRepository: &fakeBattleRepository{}, records: map[string]ai.DecisionRecord{}, acceptModels: true}
	worker, _ := New(repo, testWorkerConfig())
	if err := worker.process(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if repo.completed == nil || repo.completed.Tick != 20 || repo.saved[0].Tick != 0 {
		t.Fatal("full battle lifecycle failed")
	}
	for _, state := range repo.completed.State.Factions {
		if len(state.OwnedUpgradeIDs) != 1 || state.GoldSpent <= 0 || state.Gold < 0 {
			t.Fatalf("upgrade ownership/spending %+v", state)
		}
	}
	for _, record := range repo.records {
		if record.Status != "accepted" || record.ResolvedPlan == nil {
			t.Fatal("stub decisions not used")
		}
	}
}
