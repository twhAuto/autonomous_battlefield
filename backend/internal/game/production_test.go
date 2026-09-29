// Checks that the presentation schedule agrees with the engine's own window rules.

package game

import "testing"

func TestNextProductionMatchesEngineWindows(t *testing.T) {
	// 35 ends between windows; 30 puts the last window exactly on the tick limit.
	pastLimit, err := NewMatch("past-limit", testScenario(), 35)
	if err != nil {
		t.Fatal(err)
	}
	atLimit, err := NewMatch("at-limit", testScenario(), 30)
	if err != nil {
		t.Fatal(err)
	}
	for name, m := range map[string]*Match{"ends between windows": pastLimit, "window at limit": atLimit, "opening": doctrineMatch(t)} {
		t.Run(name, func(t *testing.T) {
			checkScheduleAgainstEngine(t, m)
		})
	}
}

type schedulePrediction struct {
	tick     uint64
	schedule ProductionSchedule
	ok       bool
}

// Advances until the match finishes, recording each window's tick, then checks
// every earlier prediction against the windows that actually ran.
func checkScheduleAgainstEngine(t *testing.T, m *Match) {
	t.Helper()
	predictions, windows := runRecordingSchedule(t, m)
	if len(windows) == 0 {
		t.Fatal("no production windows ran")
	}
	for _, p := range predictions {
		if p.schedule.Opening {
			continue
		}
		want, found := firstWindowAfter(windows, p.tick)
		if p.ok != found || (found && p.schedule.NextTick != want) {
			t.Fatalf("tick %d: predicted %+v (%t), engine ran next window at %d (%t)", p.tick, p.schedule, p.ok, want, found)
		}
	}
}

func runRecordingSchedule(t *testing.T, m *Match) ([]schedulePrediction, []uint64) {
	t.Helper()
	var predictions []schedulePrediction
	var windows []uint64
	for !m.Finished() {
		state := m.State()
		schedule, ok := NextProduction(m.scenario, state, m.maxTicks)
		predictions = append(predictions, schedulePrediction{state.Tick, schedule, ok})
		if m.OpeningDue() && (!ok || !schedule.Opening) {
			t.Fatalf("tick %d: opening due but schedule = %+v, %t", state.Tick, schedule, ok)
		}
		if m.DecisionWindowDue() && !m.OpeningDue() {
			windows = append(windows, m.DecisionTargetTick())
		}
		if _, err := m.Advance(); err != nil {
			t.Fatal(err)
		}
	}
	return predictions, windows
}

func firstWindowAfter(windows []uint64, tick uint64) (uint64, bool) {
	for _, window := range windows {
		if window > tick {
			return window, true
		}
	}
	return 0, false
}

func TestNextProductionWithoutInterval(t *testing.T) {
	if _, ok := NextProduction(Scenario{}, State{Tick: 5}, 100); ok {
		t.Fatal("a scenario without an interval has no schedule")
	}
}
