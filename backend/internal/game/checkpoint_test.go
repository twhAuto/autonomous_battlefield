// Checks that decision-window checkpoints survive a JSON round trip and resume a match
// exactly, and that a checkpoint cannot be restored into another battle.

package game

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestCheckpointsResumeMatchExactly(t *testing.T) {
	scenario := scopedUpgradeScenario()
	match, err := NewMatch("battle", scenario, 120)
	if err != nil {
		t.Fatal(err)
	}
	var reference []Transition
	for !match.Finished() {
		windowDue := match.DecisionWindowDue()
		transition, err := match.Advance()
		if err != nil {
			t.Fatal(err)
		}
		if windowDue != (transition.Checkpoint != nil) {
			t.Fatalf("tick %d: window=%v checkpoint=%v", transition.Tick, windowDue, transition.Checkpoint != nil)
		}
		reference = append(reference, transition)
	}
	if restored := requireResumesExactly(t, scenario, "battle", 120, reference); restored < 5 {
		t.Fatalf("restored %d checkpoints, want at least 5", restored)
	}
	if ids := reference[9].Checkpoint.Factions[0].ScopedUpgradeIDs; len(ids) != 1 || ids[0] != "fletching" {
		t.Fatalf("scenario did not exercise scoped upgrades: %v", ids)
	}
}

func TestRestoreMatchRejectsForeignCheckpoints(t *testing.T) {
	scenario := scopedUpgradeScenario()
	match, err := NewMatch("battle", scenario, 40)
	if err != nil {
		t.Fatal(err)
	}
	valid := match.Checkpoint()
	foreign := match.Checkpoint()
	foreign.State.BattleID = "other"
	missing := match.Checkpoint()
	missing.Factions[1].FactionID = "faction-c"
	unknown := match.Checkpoint()
	unknown.Factions[0].ScopedUpgradeIDs = []string{"retired"}
	for name, test := range map[string]struct {
		id         string
		checkpoint Checkpoint
		want       error
	}{
		"foreign battle":  {"battle", foreign, ErrInvalidCheckpoint},
		"missing faction": {"battle", missing, ErrInvalidCheckpoint},
		"unknown upgrade": {"battle", unknown, ErrInvalidCheckpoint},
		"invalid battle":  {" ", valid, ErrInvalidScenario},
	} {
		if _, err := RestoreMatch(test.id, scenario, 40, test.checkpoint); !errors.Is(err, test.want) {
			t.Errorf("%s: err = %v, want %v", name, err, test.want)
		}
	}
}

func TestCheckpointIsIndependentOfLiveMatch(t *testing.T) {
	match, err := NewMatch("battle", scopedUpgradeScenario(), 40)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := match.Checkpoint()
	checkpoint.State.Units[0].Health = -1
	checkpoint.Factions[0].ActivePlan.ActionWeights[ActionAttack] = -1
	if match.state.Units[0].Health == -1 || match.factions["faction-a"].activePlan.ActionWeights[ActionAttack] == -1 {
		t.Fatal("checkpoint shares state with the live match")
	}
}

// requireResumesExactly restores every checkpoint in reference after a JSON round trip
// and requires the restored match to repeat the following transitions through the next
// decision window. It reports how many checkpoints it restored.
func requireResumesExactly(t *testing.T, scenario Scenario, id string, maxTicks uint64, reference []Transition) int {
	t.Helper()
	restored := 0
	for index, transition := range reference {
		if transition.Checkpoint == nil {
			continue
		}
		data, err := json.Marshal(transition.Checkpoint)
		if err != nil {
			t.Fatal(err)
		}
		var checkpoint Checkpoint
		if err := json.Unmarshal(data, &checkpoint); err != nil {
			t.Fatal(err)
		}
		match, err := RestoreMatch(id, scenario, maxTicks, checkpoint)
		if err != nil {
			t.Fatalf("restore tick %d: %v", transition.Tick, err)
		}
		restored++
		requireRepeatsThroughNextWindow(t, match, transition.Tick, reference[index+1:])
	}
	return restored
}

func requireRepeatsThroughNextWindow(t *testing.T, match *Match, from uint64, following []Transition) {
	t.Helper()
	for _, want := range following {
		got, err := match.Advance()
		if err != nil {
			t.Fatalf("advance match restored at tick %d: %v", from, err)
		}
		requireSameTransition(t, from, want, got)
		if got.Checkpoint != nil {
			return
		}
	}
}

func requireSameTransition(t *testing.T, from uint64, want, got Transition) {
	t.Helper()
	wantData, _ := json.Marshal(struct {
		Transition
		Checkpoint *Checkpoint `json:"checkpoint"`
	}{want, want.Checkpoint})
	gotData, _ := json.Marshal(struct {
		Transition
		Checkpoint *Checkpoint `json:"checkpoint"`
	}{got, got.Checkpoint})
	if string(wantData) != string(gotData) {
		t.Fatalf("match restored at tick %d diverged at tick %d:\nwant %s\n got %s", from, want.Tick, wantData, gotData)
	}
}

// scopedUpgradeScenario adds a ranger-only upgrade to the blueprint, so restored
// matches must rebuild scoped upgrades for units trained after the checkpoint.
func scopedUpgradeScenario() Scenario {
	scenario := testScenario()
	scenario.Ruleset.Upgrades = append(scenario.Ruleset.Upgrades, Upgrade{ID: "fletching", Cost: 10, TargetUnit: "ranger", AttackBonus: 1})
	for i := range scenario.Factions {
		scenario.Factions[i].Blueprint.UpgradeIDs = append(scenario.Factions[i].Blueprint.UpgradeIDs, "fletching")
	}
	return scenario
}
