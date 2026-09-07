// Manages readiness, pause waits, lease heartbeats, and cancellation handoff so
// interrupted battle work can be requeued.

package worker

import (
	"context"
	"errors"
	"fmt"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"time"
)

func (w *Worker) Ready() bool { return w.ready.Load() && !w.shuttingDown.Load() }

func pausedTickResult(err error) (string, bool, error) {
	if errors.Is(err, context.Canceled) {
		return "", false, nil
	}
	return "error", true, err
}

func requeueOutcome(err error) (string, error) {
	if errors.Is(err, context.Canceled) {
		return "requeued", err
	}
	return "error", err
}

func (w *Worker) persistTickAndRequeue(ctx context.Context, lease BattleLease, transition game.Transition) error {
	w.shuttingDown.Store(true)
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.config.ShutdownTimeout)
	defer cancel()
	if err := w.repository.SaveTick(shutdownCtx, lease, transition, w.config.LeaseDuration); err != nil {
		return err
	}
	if err := w.repository.RequeueBattle(shutdownCtx, lease); err != nil {
		if errors.Is(err, ErrBattleLeaseLost) {
			return err
		}
		return fmt.Errorf("requeue during shutdown: %w", err)
	}
	w.logger.InfoContext(shutdownCtx, "battle_requeued_for_shutdown",
		"battle_id", lease.BattleID,
		"attempt", lease.Attempt,
		"persisted_tick", transition.Tick,
	)
	return context.Canceled
}

func (w *Worker) waitUntilNextTick(ctx context.Context, lease BattleLease, deadline time.Time) error {
	for {
		pausedFor, err := w.waitWhilePaused(ctx, lease)
		if err != nil {
			return err
		}
		deadline = deadline.Add(pausedFor)
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil
		}
		waitFor := min(remaining, w.config.HeartbeatInterval)
		if !wait(ctx, waitFor) {
			return context.Canceled
		}
		if time.Now().Before(deadline) {
			if err := w.repository.HeartbeatBattle(ctx, lease, w.config.LeaseDuration); err != nil {
				return err
			}
		}
	}
}

// waitWhilePaused retains and heartbeats the current lease so resuming
// continues the same simulation instead of rebuilding it from tick zero.
func (w *Worker) waitWhilePaused(ctx context.Context, lease BattleLease) (time.Duration, error) {
	repository, ok := w.repository.(pauseRepository)
	if !ok {
		return 0, nil
	}
	paused, err := repository.BattlePaused(ctx, lease)
	if err != nil || !paused {
		return 0, err
	}
	started := time.Now()
	w.logger.InfoContext(ctx, "battle_paused", "battle_id", lease.BattleID, "attempt", lease.Attempt)
	nextHeartbeat := time.Now()
	for paused {
		if !wait(ctx, min(100*time.Millisecond, w.config.HeartbeatInterval)) {
			return time.Since(started), context.Canceled
		}
		if !time.Now().Before(nextHeartbeat) {
			if err := w.repository.HeartbeatBattle(ctx, lease, w.config.LeaseDuration); err != nil {
				return time.Since(started), err
			}
			nextHeartbeat = time.Now().Add(w.config.HeartbeatInterval)
		}
		paused, err = repository.BattlePaused(ctx, lease)
		if err != nil {
			return time.Since(started), err
		}
	}
	w.logger.InfoContext(ctx, "battle_resumed", "battle_id", lease.BattleID, "attempt", lease.Attempt, "paused_ms", time.Since(started).Milliseconds())
	return time.Since(started), nil
}

func (w *Worker) requeueAfterCancellation(ctx context.Context, lease BattleLease) error {
	w.shuttingDown.Store(true)
	requeueCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.config.ShutdownTimeout)
	defer cancel()
	if err := w.repository.RequeueBattle(requeueCtx, lease); err != nil {
		if errors.Is(err, ErrBattleLeaseLost) {
			return err
		}
		return fmt.Errorf("requeue during shutdown: %w", err)
	}
	w.logger.InfoContext(requeueCtx, "battle_requeued_for_shutdown",
		"battle_id", lease.BattleID,
		"attempt", lease.Attempt,
	)
	return context.Canceled
}

func wait(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
