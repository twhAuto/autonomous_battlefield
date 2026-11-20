// Checks replay segments re-simulate a finished battle exactly from stored checkpoints
// and refuse active battles, out-of-range ticks and missing checkpoints.

package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/game"
)

const replayBattleID = "0f5c3a0e-8f6d-4c1e-9d3b-2a7f6e5d4c3b"

// replayFixture runs a scripted battle and stores a frame for each window transition,
// as the engine worker does.
func replayFixture(t *testing.T, ticks uint64) (*fakeBattleRepo, []game.Transition) {
	t.Helper()
	scenario := serviceTestScenario([2]FactionIdentity{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}})
	match, err := game.NewMatch(replayBattleID, scenario, ticks)
	if err != nil {
		t.Fatal(err)
	}
	repo := &fakeBattleRepo{}
	reference := []game.Transition{}
	for !match.Finished() {
		transition, err := match.Advance()
		if err != nil {
			t.Fatal(err)
		}
		if transition.Checkpoint != nil {
			repo.frames = append(repo.frames, StoredReplayFrame{Tick: transition.Tick, Checkpoint: *transition.Checkpoint, Events: transition.Events})
		}
		reference = append(reference, transition)
	}
	final := reference[len(reference)-1]
	repo.run = &BattleRun{ID: replayBattleID, ScenarioSnapshot: scenario, RequestedTicks: ticks, CurrentTick: final.Tick, CurrentState: final.State, Status: "completed", Attempt: 2, Result: final.Result}
	return repo, reference
}

func TestBattleReplaySegmentsMatchTheRecordedBattle(t *testing.T) {
	repo, reference := replayFixture(t, 60)
	svc := NewService(repo, nil, nil, 100, Options{})
	byTick := map[uint64]game.Transition{}
	for _, transition := range reference {
		byTick[transition.Tick] = transition
	}
	segments := 0
	for tick := uint64(10); ; segments++ {
		replay, err := svc.GetBattleReplay(context.Background(), replayBattleID, tick)
		if err != nil || replay.FromTick != tick || replay.Attempt != 2 || repo.frameAttempt != 2 || replay.FinalTick != 60 {
			t.Fatalf("tick %d: segment %+v, %v", tick, replay, err)
		}
		requireRecordedFrames(t, replay, byTick)
		if replay.NextTick == nil {
			break
		}
		if *replay.NextTick != replay.ToTick+1 || replay.ToTick-replay.FromTick != 9 {
			t.Fatalf("segment %d-%d continues at %d", replay.FromTick, replay.ToTick, *replay.NextTick)
		}
		tick = *replay.NextTick
	}
	if segments != 5 {
		t.Fatalf("replayed %d full segments before the final tick, want 5", segments)
	}
	middle, err := svc.GetBattleReplay(context.Background(), replayBattleID, 25)
	if err != nil || middle.FromTick != 20 || middle.ToTick != 29 {
		t.Fatalf("mid-segment tick: %+v, %v", middle, err)
	}
}

// requireRecordedFrames requires one frame per segment tick, identical to the battle.
func requireRecordedFrames(t *testing.T, replay BattleReplay, byTick map[uint64]game.Transition) {
	t.Helper()
	if len(replay.Frames) != int(replay.ToTick-replay.FromTick+1) {
		t.Fatalf("segment %d-%d has %d frames", replay.FromTick, replay.ToTick, len(replay.Frames))
	}
	for _, frame := range replay.Frames {
		want := byTick[frame.Tick]
		if jsonOf(t, frame.State) != jsonOf(t, want.State) || jsonOf(t, frame.Events) != jsonOf(t, want.Events) {
			t.Fatalf("replayed tick %d differs from the recorded battle", frame.Tick)
		}
	}
}

func TestBattleReplayStopsAtAStoppedBattlesLastTick(t *testing.T) {
	repo, _ := replayFixture(t, 60)
	repo.run.Status, repo.run.CurrentTick = "stopped", 34
	replay, err := NewService(repo, nil, nil, 100, Options{}).GetBattleReplay(context.Background(), replayBattleID, 34)
	if err != nil || replay.FromTick != 30 || replay.ToTick != 34 || replay.NextTick != nil || replay.FinalTick != 34 {
		t.Fatalf("stopped replay: %+v, %v", replay, err)
	}
}

func TestBattleReplayRejectsUnavailableSegments(t *testing.T) {
	for name, test := range map[string]struct {
		change func(*fakeBattleRepo)
		tick   uint64
		want   error
	}{
		"running battle":     {func(r *fakeBattleRepo) { r.run.Status = "running" }, 10, ErrReplayUnavailable},
		"queued battle":      {func(r *fakeBattleRepo) { r.run.Status = "queued" }, 0, ErrReplayUnavailable},
		"after final tick":   {func(*fakeBattleRepo) {}, 61, ErrInvalidBattle},
		"before checkpoints": {func(*fakeBattleRepo) {}, 5, ErrReplayUnavailable},
		"no checkpoints":     {func(r *fakeBattleRepo) { r.frames = nil }, 30, ErrReplayUnavailable},
		"checkpoint gap":     {func(r *fakeBattleRepo) { r.frames = append(r.frames[:2], r.frames[3:]...) }, 35, ErrReplayUnavailable},
		"missing battle":     {func(r *fakeBattleRepo) { r.run = nil }, 10, ErrBattleNotFound},
		"read failure":       {func(r *fakeBattleRepo) { r.frameErr = errors.New("database down") }, 10, nil},
	} {
		t.Run(name, func(t *testing.T) {
			repo, _ := replayFixture(t, 60)
			test.change(repo)
			_, err := NewService(repo, nil, nil, 100, Options{}).GetBattleReplay(context.Background(), replayBattleID, test.tick)
			if err == nil || test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("err = %v, want %v", err, test.want)
			}
		})
	}
}

func TestBattleReplayRejectsAForeignCheckpoint(t *testing.T) {
	repo, _ := replayFixture(t, 60)
	repo.frames[0].Checkpoint.State.BattleID = "other"
	_, err := NewService(repo, nil, nil, 100, Options{}).GetBattleReplay(context.Background(), replayBattleID, 12)
	if !errors.Is(err, game.ErrInvalidCheckpoint) || errors.Is(err, ErrReplayUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestBattleReplayRejectsInvalidIDs(t *testing.T) {
	if _, err := NewService(&fakeBattleRepo{}, nil, nil, 100, Options{}).GetBattleReplay(context.Background(), "not-a-uuid", 0); !errors.Is(err, ErrInvalidBattle) {
		t.Fatalf("err = %v", err)
	}
}

func jsonOf(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
