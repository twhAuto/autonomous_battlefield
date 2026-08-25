// Package worker orchestrates durable queued battles around the pure engine.
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	battle "github.com/twh09/autonomous_battlefield/backend/internal/battle/simulation"
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

	battle, err := battle.NewBattle(job.Lease.BattleID, job.Scenario, job.RequestedTicks)
	if err != nil {
		w.logger.ErrorContext(ctx, "battle_scenario_invalid",
			"battle_id", job.Lease.BattleID,
			"attempt", job.Lease.Attempt,
			"error", err,
		)
		failureCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.config.ShutdownTimeout)
		defer cancel()
		if failErr := w.repository.FailBattle(failureCtx, job.Lease, "invalid_scenario", err.Error()); failErr != nil {
			return failErr
		}
		w.logger.InfoContext(failureCtx, "battle_failed",
			"battle_id", job.Lease.BattleID,
			"attempt", job.Lease.Attempt,
			"error_code", "invalid_scenario",
		)
		outcome = "failed"
		return nil
	}

	for {
		if ctx.Err() != nil {
			resultErr = w.requeueAfterCancellation(ctx, job.Lease)
			if errors.Is(resultErr, context.Canceled) {
				outcome = "requeued"
			}
			return resultErr
		}
		tickStarted := time.Now()
		transition, err := battle.Advance()
		if err != nil {
			return err
		}
		telemetry.ObserveEngineTick()
		if transition.Finished {
			completeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.config.ShutdownTimeout)
			err := w.repository.CompleteBattle(completeCtx, job.Lease, transition)
			if err == nil {
				outcome = "completed"
				var winner any
				if transition.Result.WinnerFactionID != nil {
					winner = *transition.Result.WinnerFactionID
				}
				w.logger.InfoContext(completeCtx, "battle_completed",
					"battle_id", job.Lease.BattleID,
					"attempt", job.Lease.Attempt,
					"ticks_run", transition.Tick,
					"winner_faction_id", winner,
					"reason", transition.Result.Reason,
				)
			}
			cancel()
			return err
		}
		if ctx.Err() != nil {
			resultErr = w.persistTickAndRequeue(ctx, job.Lease, transition)
			if errors.Is(resultErr, context.Canceled) {
				outcome = "requeued"
			}
			return resultErr
		}
		if err := w.repository.SaveTick(ctx, job.Lease, transition, w.config.LeaseDuration); err != nil {
			return err
		}
		if ctx.Err() != nil {
			resultErr = w.requeueAfterCancellation(ctx, job.Lease)
			if errors.Is(resultErr, context.Canceled) {
				outcome = "requeued"
			}
			return resultErr
		}
		if duration := battle.TickDuration(); duration > 0 {
			if err := w.waitUntilNextTick(ctx, job.Lease, tickStarted.Add(duration)); err != nil {
				if errors.Is(err, context.Canceled) {
					continue
				}
				return err
			}
		}
	}
}

func (w *Worker) persistTickAndRequeue(ctx context.Context, lease BattleLease, transition battle.Transition) error {
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
