// Checks opening decisions are recorded before combat and model-controlled doctrine
// battles can complete.

package worker

import (
	"github.com/twh09/autonomous_battlefield/backend/internal/testutil"

	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
)

func TestOpeningIsRecordedAndPersistedBeforeCombat(t *testing.T) {
	for _, model := range []bool{false, true} {
		checkOpeningPersistence(t, model)
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
	worker, _ := New(repo, repo, testWorkerConfig())
	{
		err := worker.process(context.Background(), job)
		testutil.Require(t, err == nil, func() { t.Fatal(err) })
	}
	testutil.Require(t, !(repo.completed == nil || repo.completed.Tick != 20 || repo.saved[0].Tick != 0), func() { t.Fatal("full battle lifecycle failed") })
	for _, state := range repo.completed.State.Factions {
		testutil.Require(t, !(len(state.OwnedUpgradeIDs) != 1 || state.GoldSpent <= 0 || state.Gold < 0), func() { t.Fatalf("upgrade ownership/spending %+v", state) })
	}
	for _, record := range repo.records {
		testutil.Require(t, !(record.Status != "accepted" || record.ResolvedPlan == nil), func() { t.Fatal("stub decisions not used") })
	}
}

func checkOpeningPersistence(t *testing.T, model bool) {
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
	worker, _ := New(repo, repo, config)
	simulation, err := game.NewMatch(job.Lease.BattleID, job.Scenario, job.RequestedTicks)
	testutil.Require(t, err == nil, func() { t.Fatal(err) })
	_, finished, err := worker.runTick(context.Background(), job.Lease, simulation)
	testutil.Require(t, !(err != nil || finished), func() { t.Fatalf("opening %v %v", finished, err) })
	testutil.Require(t, !(len(repo.saved) != 1 || repo.saved[0].Tick != 0 || !repo.saved[0].State.OpeningComplete), func() { t.Fatal("opening not durable before tick one") })
	testutil.Require(t, len(repo.records) == 2, func() { t.Fatal("missing simultaneous decisions") })
	for _, record := range repo.records {
		testutil.Require(t, !(record.TargetTick != 0 || !record.Observation.Opening || len(record.Observation.View.State.Units) != 0 || record.ResolvedPlan == nil), func() { t.Fatal("opening decisions not frozen") })
		expected := "Defend"
		if record.FactionID == job.Scenario.Factions[0].ID {
			expected = "Scout"
		}
		owner, opponent := job.Scenario.Factions[1], job.Scenario.Factions[0]
		if record.FactionID == job.Scenario.Factions[0].ID {
			owner, opponent = opponent, owner
		}
		raw, _ := json.Marshal(record.Observation)
		testutil.Require(t, !(record.Observation.Background != owner.Background || record.Observation.FactionName != owner.Name || strings.Contains(string(raw), opponent.Background)), func() { t.Fatal("identity isolation", string(raw)) })
		testutil.Require(t, record.Observation.Doctrine == expected, func() { t.Fatal("wrong doctrine exposed") })
		testutil.Require(t, !(record.ControllerType == ai.ControllerModel && (record.Status != "fallback" || record.FallbackSource != "scripted_fallback")), func() { t.Fatal("opening timeout fallback") })
	}
	_, _, err = worker.runTick(context.Background(), job.Lease, simulation)
	testutil.Require(t, err == nil, func() { t.Fatal(err) })
	testutil.Require(t, !(simulation.State().Tick != 1 || len(repo.records) != 2), func() { t.Fatal("opening repeated") })
}
