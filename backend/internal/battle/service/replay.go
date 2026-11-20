// Rebuilds a finished battle's replay segments by resuming the engine from the stored
// decision-window checkpoint and re-simulating the ticks before the next window.

package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/game"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/observability/tracing"
	"go.opentelemetry.io/otel/attribute"
)

// ErrReplayUnavailable reports a battle that is still active, or whose final attempt
// saved no checkpoint for the requested segment, such as one finished before replays.
var ErrReplayUnavailable = errors.New("battle replay unavailable")

type ReplayService interface {
	GetBattleReplay(context.Context, string, uint64) (BattleReplay, error)
}

// StoredReplayFrame is a decision-window checkpoint saved with its own tick's events.
type StoredReplayFrame struct {
	Tick       uint64
	Checkpoint game.Checkpoint
	Events     []game.Event
}

type ReplayFrame struct {
	Tick   uint64       `json:"tick"`
	State  game.State   `json:"state"`
	Events []game.Event `json:"events"`
}

// BattleReplay is one segment: a checkpoint tick and the ticks before the next window.
// NextTick starts the following segment and is nil at the battle's final tick.
type BattleReplay struct {
	BattleID  string        `json:"battleId"`
	Attempt   int           `json:"attempt"`
	FinalTick uint64        `json:"finalTick"`
	FromTick  uint64        `json:"fromTick"`
	ToTick    uint64        `json:"toTick"`
	NextTick  *uint64       `json:"nextTick"`
	Frames    []ReplayFrame `json:"frames"`
}

var replayableStatuses = map[string]bool{"completed": true, "stopped": true, "failed": true}

// GetBattleReplay returns the segment containing tick from the battle's final attempt.
// Plans apply only at windows, so re-simulating within a segment needs none.
func (s *battleService) GetBattleReplay(ctx context.Context, id string, tick uint64) (replay BattleReplay, err error) {
	run, err := s.GetBattle(ctx, id)
	if err != nil {
		return BattleReplay{}, err
	}
	if !replayableStatuses[run.Status] {
		return BattleReplay{}, fmt.Errorf("%w: battle %q is %s", ErrReplayUnavailable, run.ID, run.Status)
	}
	if tick > run.CurrentTick {
		return BattleReplay{}, ErrInvalidBattle
	}
	ctx, span := tracing.StartSpan(ctx, "battlefield.api.battle_replay",
		attribute.String("battlefield.battle_id", run.ID),
		attribute.Int("battlefield.attempt", run.Attempt),
		attribute.Int64("battlefield.tick", int64(tick)),
	)
	defer func() { tracing.EndSpan(span, err) }()
	frame, err := s.battles.GetReplayFrame(ctx, run.ID, run.Attempt, tick)
	if err != nil {
		return BattleReplay{}, err
	}
	match, err := game.RestoreMatch(run.ID, run.ScenarioSnapshot, run.RequestedTicks, frame.Checkpoint)
	if err != nil {
		return BattleReplay{}, fmt.Errorf("restore battle %q replay at tick %d: %w", run.ID, frame.Tick, err)
	}
	frames := []ReplayFrame{{Tick: frame.Tick, State: frame.Checkpoint.State, Events: frame.Events}}
	for current := frame.Tick; current < run.CurrentTick && !match.Finished() && !match.DecisionWindowDue(); {
		transition, err := match.AdvanceWithPlans(nil)
		if err != nil {
			return BattleReplay{}, fmt.Errorf("re-simulate battle %q tick %d: %w", run.ID, current+1, err)
		}
		current = transition.Tick
		frames = append(frames, ReplayFrame{Tick: transition.Tick, State: transition.State, Events: transition.Events})
	}
	last := frames[len(frames)-1].Tick
	if tick > last {
		// A missing checkpoint left a gap; re-simulating across a window would need plans.
		return BattleReplay{}, fmt.Errorf("%w: battle %q has no checkpoint for tick %d", ErrReplayUnavailable, run.ID, tick)
	}
	replay = BattleReplay{BattleID: run.ID, Attempt: run.Attempt, FinalTick: run.CurrentTick, FromTick: frame.Tick, ToTick: last, Frames: frames}
	if last < run.CurrentTick {
		next := last + 1
		replay.NextTick = &next
	}
	return replay, nil
}
