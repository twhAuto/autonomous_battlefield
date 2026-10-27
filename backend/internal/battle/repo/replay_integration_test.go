// Checks replay checkpoints are stored with their tick only for the lease owner, keep
// the first write on retry, and are found by attempt and nearest earlier tick.

package repo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/battle/queue"
	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
	battle "github.com/twh09/autonomous_battlefield/backend/internal/game"
)

func TestReplayFramesAgainstPostgres(t *testing.T) {
	ctx, db := integrationDatabase(t)
	battleID := randomUUID(t)
	t.Cleanup(func() { _, _ = db.Exec(context.Background(), `DELETE FROM battle_runs WHERE id = $1`, battleID) })
	repo := NewPostgres(db)
	job := createAndClaimBattle(t, ctx, repo, battleID)

	opening := checkpointTransition(battleID, 0, 7)
	if err := repo.SaveTick(ctx, job.Lease, opening, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	retry := checkpointTransition(battleID, 0, 99)
	if err := repo.SaveTick(ctx, job.Lease, retry, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	plain := battle.Transition{Tick: 1, Events: []battle.Event{{Sequence: 9, Tick: 1, Type: "tick_completed", Data: map[string]any{}}}}
	if err := repo.SaveTick(ctx, job.Lease, plain, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	stale := job.Lease
	stale.Owner = "worker-stale"
	if err := repo.SaveTick(ctx, stale, checkpointTransition(battleID, 1, 3), 30*time.Second); !errors.Is(err, queue.ErrLeaseLost) {
		t.Fatalf("stale SaveTick() error = %v", err)
	}
	final := checkpointTransition(battleID, 2, 11)
	final.Finished, final.Result = true, &battle.Result{Reason: "tick_limit", TicksRun: 2}
	if err := repo.CompleteBattle(ctx, job.Lease, final); err != nil {
		t.Fatal(err)
	}

	for tick, want := range map[uint64]uint64{0: 0, 1: 0, 2: 2, 50: 2} {
		frame, err := repo.GetReplayFrame(ctx, battleID, job.Lease.Attempt, tick)
		if err != nil || frame.Tick != want {
			t.Fatalf("frame at %d = %+v, %v; want tick %d", tick, frame, err, want)
		}
	}
	frame, _ := repo.GetReplayFrame(ctx, battleID, job.Lease.Attempt, 1)
	if frame.Checkpoint.Sequence != 7 || frame.Checkpoint.State.BattleID != battleID || len(frame.Events) != 1 || frame.Events[0].Type != "plan_applied" {
		t.Fatalf("stored frame = %+v", frame)
	}
	if _, err := repo.GetReplayFrame(ctx, battleID, job.Lease.Attempt+1, 2); !errors.Is(err, battleservice.ErrReplayUnavailable) {
		t.Fatalf("other attempt error = %v", err)
	}
}

func checkpointTransition(battleID string, tick, sequence uint64) battle.Transition {
	state := battle.State{BattleID: battleID, Tick: tick, Factions: []battle.FactionState{{FactionID: "faction-a"}, {FactionID: "faction-b"}}}
	return battle.Transition{
		Tick: tick, State: state,
		Events:     []battle.Event{{Sequence: sequence, Tick: tick, Type: "plan_applied", Data: map[string]any{"factionId": "faction-a"}}},
		Checkpoint: &battle.Checkpoint{State: state, Sequence: sequence, Started: true, Factions: []battle.FactionCheckpoint{{FactionID: "faction-a"}, {FactionID: "faction-b"}}},
	}
}
