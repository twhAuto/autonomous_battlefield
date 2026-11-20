// Checks that a frontier battle resumed from any decision-window checkpoint, after a
// JSON round trip, repeats the uninterrupted battle through the next window.

package catalog

import (
	"encoding/json"
	"testing"

	"github.com/twhAuto/autonomous_battlefield/backend/gamedata"
	service "github.com/twhAuto/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/game"
)

func TestFrontierCheckpointsResumeExactly(t *testing.T) {
	skipSequentialBattleUnderRace(t)
	c, err := LoadCatalog(gamedata.Files)
	if err != nil {
		t.Fatal(err)
	}
	s, err := c.Resolve("frontier-siege", [2]service.FactionIdentity{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}})
	if err != nil {
		t.Fatal(err)
	}
	reference := frontierTransitions(t, s, 400)
	if reference[0].Tick != 0 || reference[0].Checkpoint == nil || !reference[0].Checkpoint.State.OpeningComplete {
		t.Fatal("the opening transition carries no checkpoint")
	}
	restored := 0
	for index, transition := range reference {
		if transition.Checkpoint == nil {
			continue
		}
		match, err := game.RestoreMatch("replay", s, 400, roundTrip(t, *transition.Checkpoint))
		if err != nil {
			t.Fatalf("restore tick %d: %v", transition.Tick, err)
		}
		restored++
		requireRepeatsThroughNextWindow(t, match, transition.Tick, reference[index+1:])
	}
	if restored < 10 {
		t.Fatalf("restored %d checkpoints, want at least 10", restored)
	}
}

func requireRepeatsThroughNextWindow(t *testing.T, match *game.Match, from uint64, following []game.Transition) {
	t.Helper()
	for _, want := range following {
		got, err := match.Advance()
		if err != nil {
			t.Fatal(err)
		}
		if transitionJSON(t, want) != transitionJSON(t, got) {
			t.Fatalf("battle restored at tick %d diverged at tick %d", from, want.Tick)
		}
		if got.Checkpoint != nil {
			return
		}
	}
}

func frontierTransitions(t *testing.T, s game.Scenario, ticks uint64) []game.Transition {
	t.Helper()
	match, err := game.NewMatch("replay", s, ticks)
	if err != nil {
		t.Fatal(err)
	}
	var transitions []game.Transition
	for !match.Finished() {
		transition, err := match.Advance()
		if err != nil {
			t.Fatal(err)
		}
		transitions = append(transitions, transition)
	}
	return transitions
}

func roundTrip(t *testing.T, checkpoint game.Checkpoint) game.Checkpoint {
	t.Helper()
	data, err := json.Marshal(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	var decoded game.Checkpoint
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

// transitionJSON includes the checkpoint, which the transition's own JSON omits.
func transitionJSON(t *testing.T, transition game.Transition) string {
	t.Helper()
	data, err := json.Marshal(struct {
		game.Transition
		Checkpoint *game.Checkpoint `json:"checkpoint"`
	}{transition, transition.Checkpoint})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
