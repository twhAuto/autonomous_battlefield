// Package worker orchestrates durable queued battles around the pure engine.
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/telemetry"
)

type Config struct {
	WorkerID          string
	PollInterval      time.Duration
	LeaseDuration     time.Duration
	HeartbeatInterval time.Duration
	ShutdownTimeout   time.Duration
	MaxAttempts       int
}

type Worker struct {
	repository   Repository
	config       Config
	logger       *slog.Logger
	ready        atomic.Bool
	shuttingDown atomic.Bool
}

func New(repository Repository, config Config) (*Worker, error) {
	return NewWithLogger(repository, config, slog.Default())
}

func NewWithLogger(repository Repository, config Config, logger *slog.Logger) (*Worker, error) {
	if config.WorkerID == "" || config.PollInterval <= 0 || config.LeaseDuration <= 0 || config.HeartbeatInterval <= 0 || config.HeartbeatInterval >= config.LeaseDuration || config.ShutdownTimeout <= 0 || config.MaxAttempts <= 0 {
		return nil, errors.New("invalid worker configuration")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{repository: repository, config: config, logger: logger.With("worker_id", config.WorkerID)}, nil
}

func (w *Worker) Ready() bool { return w.ready.Load() && !w.shuttingDown.Load() }

func (w *Worker) Run(ctx context.Context) error {
	w.ready.Store(true)
	telemetry.SetEngineReady(true)
	w.logger.InfoContext(ctx, "battle_worker_ready",
		"poll_interval", w.config.PollInterval,
		"lease_duration", w.config.LeaseDuration,
		"max_attempts", w.config.MaxAttempts,
	)
	defer func() {
		w.ready.Store(false)
		telemetry.SetEngineReady(false)
		w.logger.Info("battle_worker_stopped")
	}()
	for {
		if err := ctx.Err(); err != nil {
			w.shuttingDown.Store(true)
			return nil
		}
		job, err := w.repository.ClaimBattle(ctx, w.config.WorkerID, w.config.LeaseDuration, w.config.MaxAttempts)
		if errors.Is(err, ErrNoBattleAvailable) {
			if !wait(ctx, w.config.PollInterval) {
				w.shuttingDown.Store(true)
				return nil
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("claim battle: %w", err)
		}
		telemetry.ObserveEngineClaim(job.Lease.Attempt > 1)
		w.logger.InfoContext(ctx, "battle_claimed",
			"battle_id", job.Lease.BattleID,
			"attempt", job.Lease.Attempt,
			"reclaimed", job.Lease.Attempt > 1,
			"scenario_id", job.Scenario.ID,
			"requested_ticks", job.RequestedTicks,
		)
		if err := w.process(ctx, job); err != nil {
			attributes := []any{"battle_id", job.Lease.BattleID, "attempt", job.Lease.Attempt, "error", err}
			switch {
			case errors.Is(err, context.Canceled):
				// Graceful requeue paths log their successful handoff directly.
			case errors.Is(err, ErrBattleLeaseLost):
				w.logger.WarnContext(ctx, "battle_lease_lost", attributes...)
			default:
				w.logger.ErrorContext(ctx, "battle_processing_stopped", attributes...)
			}
		}
	}
}

func (w *Worker) process(ctx context.Context, job *BattleJob) (resultErr error) {
	started := time.Now()
	outcome := "error"
	defer func() {
		if errors.Is(resultErr, ErrBattleLeaseLost) {
			outcome = "lease_lost"
		}
		telemetry.ObserveEngineBattle(outcome, time.Since(started))
	}()

	simulation, err := game.NewMatch(job.Lease.BattleID, job.Scenario, job.RequestedTicks)
	if err != nil {
		resultErr = w.failInvalidScenario(ctx, job.Lease, err)
		if resultErr == nil {
			outcome = "failed"
		}
		return resultErr
	}
	outcome, resultErr = w.runBattle(ctx, job, simulation)
	return resultErr
}

func (w *Worker) runBattle(ctx context.Context, job *BattleJob, simulation *game.Match) (string, error) {
	for {
		outcome, finished, err := w.runTick(ctx, job.Lease, simulation)
		if finished {
			return outcome, err
		}
	}
}

func (w *Worker) runTick(ctx context.Context, lease BattleLease, simulation *game.Match) (string, bool, error) {
	if ctx.Err() != nil {
		outcome, err := requeueOutcome(w.requeueAfterCancellation(ctx, lease))
		return outcome, true, err
	}
	tickStarted := time.Now()
	transition, err := simulation.Advance()
	if err != nil {
		return "error", true, err
	}
	telemetry.ObserveEngineTick()
	if transition.Finished {
		outcome, err := w.completeBattle(ctx, lease, transition)
		return outcome, true, err
	}
	if ctx.Err() != nil {
		outcome, err := requeueOutcome(w.persistTickAndRequeue(ctx, lease, transition))
		return outcome, true, err
	}
	if err := w.repository.SaveTick(ctx, lease, transition, w.config.LeaseDuration); err != nil {
		return "error", true, err
	}
	if ctx.Err() != nil {
		outcome, err := requeueOutcome(w.requeueAfterCancellation(ctx, lease))
		return outcome, true, err
	}
	if duration := simulation.TickDuration(); duration > 0 {
		if err := w.waitUntilNextTick(ctx, lease, tickStarted.Add(duration)); err != nil {
			if errors.Is(err, context.Canceled) {
				return "", false, nil
			}
			return "error", true, err
		}
	}
	return "", false, nil
}

func (w *Worker) failInvalidScenario(ctx context.Context, lease BattleLease, scenarioErr error) error {
	w.logger.ErrorContext(ctx, "battle_scenario_invalid",
		"battle_id", lease.BattleID,
		"attempt", lease.Attempt,
		"error", scenarioErr,
	)
	failureCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.config.ShutdownTimeout)
	defer cancel()
	if err := w.repository.FailBattle(failureCtx, lease, "invalid_scenario", scenarioErr.Error()); err != nil {
		return err
	}
	w.logger.InfoContext(failureCtx, "battle_failed",
		"battle_id", lease.BattleID,
		"attempt", lease.Attempt,
		"error_code", "invalid_scenario",
	)
	return nil
}

func (w *Worker) completeBattle(ctx context.Context, lease BattleLease, transition game.Transition) (string, error) {
	completeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.config.ShutdownTimeout)
	defer cancel()
	if err := w.repository.CompleteBattle(completeCtx, lease, transition); err != nil {
		return "error", err
	}
	var winner any
	if transition.Result.WinnerFactionID != nil {
		winner = *transition.Result.WinnerFactionID
	}
	w.logger.InfoContext(completeCtx, "battle_completed",
		"battle_id", lease.BattleID,
		"attempt", lease.Attempt,
		"ticks_run", transition.Tick,
		"winner_faction_id", winner,
		"reason", transition.Result.Reason,
	)
	return "completed", nil
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
