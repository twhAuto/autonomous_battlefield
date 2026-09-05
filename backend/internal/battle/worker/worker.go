// Package worker orchestrates durable queued battles around the pure engine.
package worker

import (
	"context"
	"errors"
	"fmt"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"log/slog"
	"sync/atomic"
	"time"
)

type Config struct {
	KnowledgeRetrieval ai.KnowledgeLimits
	WorkerID           string
	PollInterval       time.Duration
	LeaseDuration      time.Duration
	HeartbeatInterval  time.Duration
	ShutdownTimeout    time.Duration
	MaxAttempts        int
	DecisionTimeout    time.Duration
	CapabilitySecret   string
}

type Worker struct {
	repository   Repository
	config       Config
	logger       *slog.Logger
	decisions    ai.DecisionRepository
	ready        atomic.Bool
	shuttingDown atomic.Bool
}

func New(repository Repository, config Config) (*Worker, error) {
	return NewWithLogger(repository, config, slog.Default())
}

func NewWithLogger(repository Repository, config Config, logger *slog.Logger) (*Worker, error) {
	if config.KnowledgeRetrieval == (ai.KnowledgeLimits{}) {
		config.KnowledgeRetrieval = ai.DefaultKnowledgeLimits()
	}
	if err := config.KnowledgeRetrieval.Validate(); err != nil {
		return nil, err
	}
	if config.DecisionTimeout == 0 {
		config.DecisionTimeout = 2 * time.Minute
	}
	if config.CapabilitySecret == "" {
		config.CapabilitySecret = "local-development-capability-secret-change-me"
	}
	if config.WorkerID == "" || config.PollInterval <= 0 || config.LeaseDuration <= 0 || config.HeartbeatInterval <= 0 || config.HeartbeatInterval >= config.LeaseDuration || config.ShutdownTimeout <= 0 || config.MaxAttempts <= 0 {
		return nil, errors.New("invalid worker configuration")
	}
	if logger == nil {
		logger = slog.Default()
	}
	worker := &Worker{repository: repository, config: config, logger: logger.With("worker_id", config.WorkerID)}
	if decisions, ok := repository.(ai.DecisionRepository); ok {
		worker.decisions = decisions
	}
	return worker, nil
}

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
		jobContext := telemetry.ContextWithTrace(ctx, job.Traceparent, job.Tracestate)
		jobContext, span := telemetry.StartConsumerSpan(jobContext, "battlefield.engine.process",
			attribute.String("battlefield.battle_id", job.Lease.BattleID),
			attribute.Int("battlefield.attempt", job.Lease.Attempt),
		)
		w.logger.InfoContext(jobContext, "battle_claimed",
			"battle_id", job.Lease.BattleID,
			"attempt", job.Lease.Attempt,
			"reclaimed", job.Lease.Attempt > 1,
			"scenario_id", job.Scenario.ID,
			"requested_ticks", job.RequestedTicks,
		)
		processErr := w.process(jobContext, job)
		if processErr != nil {
			span.RecordError(processErr)
			attributes := []any{"battle_id", job.Lease.BattleID, "attempt", job.Lease.Attempt, "error", processErr}
			switch {
			case errors.Is(processErr, context.Canceled):
				// Graceful requeue paths log their successful handoff directly.
			case errors.Is(processErr, ErrBattleLeaseLost):
				w.logger.WarnContext(jobContext, "battle_lease_lost", attributes...)
			default:
				w.logger.ErrorContext(jobContext, "battle_processing_stopped", attributes...)
			}
		}
		span.End()
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
	if _, err := w.waitWhilePaused(ctx, lease); err != nil {
		return pausedTickResult(err)
	}
	tickStarted := time.Now()
	transition, err := w.advanceSimulation(ctx, lease, simulation)
	if err != nil {
		return "error", true, err
	}
	pausedFor, err := w.waitWhilePaused(ctx, lease)
	if err != nil {
		return pausedTickResult(err)
	}
	tickStarted = tickStarted.Add(pausedFor)
	telemetry.ObserveEngineTick()
	if transition.Finished {
		outcome, err := w.completeBattle(ctx, lease, transition)
		return outcome, true, err
	}
	return w.persistRunningTick(ctx, lease, simulation, transition, tickStarted)
}

func (w *Worker) advanceSimulation(ctx context.Context, lease BattleLease, simulation *game.Match) (game.Transition, error) {
	if !simulation.DecisionWindowDue() || w.decisions == nil {
		return simulation.Advance()
	}
	plans, err := w.resolveDecisionWindow(ctx, lease, simulation)
	if err != nil {
		return game.Transition{}, err
	}
	return simulation.AdvanceWithPlans(plans)
}

func (w *Worker) persistRunningTick(ctx context.Context, lease BattleLease, simulation *game.Match, transition game.Transition, tickStarted time.Time) (string, bool, error) {
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
