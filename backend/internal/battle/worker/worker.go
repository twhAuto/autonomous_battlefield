// Package worker orchestrates durable queued battles around the pure engine.
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/battle/queue"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/observability/metrics"
	"github.com/twh09/autonomous_battlefield/backend/internal/observability/tracing"
	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/sync/errgroup"
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
	// Concurrency is how many battles run at once, one claim loop each; zero means one.
	Concurrency int
}

type Worker struct {
	repository   Repository
	config       Config
	logger       *slog.Logger
	decisions    DecisionRepository
	ready        atomic.Bool
	shuttingDown atomic.Bool
}

// New builds an engine worker. decisions runs AI decision windows; pass nil only for a
// worker that should advance battles on scripted plans alone.
func New(repository Repository, decisions DecisionRepository, config Config) (*Worker, error) {
	return NewWithLogger(repository, decisions, config, slog.Default())
}

func NewWithLogger(repository Repository, decisions DecisionRepository, config Config, logger *slog.Logger) (*Worker, error) {
	if config.KnowledgeRetrieval == (ai.KnowledgeLimits{}) {
		config.KnowledgeRetrieval = ai.DefaultKnowledgeLimits()
	}
	if err := config.KnowledgeRetrieval.Validate(); err != nil {
		return nil, err
	}
	if config.DecisionTimeout == 0 {
		config.DecisionTimeout = 2 * time.Minute
	}
	if config.Concurrency == 0 {
		config.Concurrency = 1
	}
	if config.Concurrency < 0 || config.WorkerID == "" || config.PollInterval <= 0 || config.LeaseDuration <= 0 || config.HeartbeatInterval <= 0 || config.HeartbeatInterval >= config.LeaseDuration || config.ShutdownTimeout <= 0 || config.MaxAttempts <= 0 {
		return nil, errors.New("invalid worker configuration")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{repository: repository, decisions: decisions, config: config, logger: logger.With("worker_id", config.WorkerID)}, nil
}

func (w *Worker) Run(ctx context.Context) error {
	expiryCtx, cancelExpiry := context.WithCancel(ctx)
	expiryDone := make(chan struct{})
	go w.sweepExpiredBattles(expiryCtx, expiryDone)
	defer func() { cancelExpiry(); <-expiryDone }()

	w.ready.Store(true)
	metrics.SetEngineReady(true)
	metrics.SetEngineSlots(w.config.Concurrency)
	w.logger.InfoContext(ctx, "battle_worker_ready",
		"concurrency", w.config.Concurrency,
		"poll_interval", w.config.PollInterval,
		"lease_duration", w.config.LeaseDuration,
		"max_attempts", w.config.MaxAttempts,
	)
	defer func() {
		w.ready.Store(false)
		metrics.SetEngineReady(false)
		w.logger.Info("battle_worker_stopped")
	}()
	// Each slot claims and runs one battle at a time under the shared worker ID; leases
	// are per battle. A claim failure in one slot cancels the others, which requeue
	// their battles before Run returns the error.
	group, groupContext := errgroup.WithContext(ctx)
	for slot := range w.config.Concurrency {
		group.Go(func() error { return w.claimBattles(groupContext, slot+1) })
	}
	return group.Wait()
}

func (w *Worker) sweepExpiredBattles(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	for {
		sweepCtx, cancelSweep := context.WithTimeout(ctx, 10*time.Second)
		err := w.repository.ExpireBattles(sweepCtx)
		cancelSweep()
		if err != nil && ctx.Err() == nil {
			w.logger.ErrorContext(ctx, "battle_expiry_failed", "error", err)
		}
		if !wait(ctx, time.Minute) {
			return
		}
	}
}

func (w *Worker) claimBattles(ctx context.Context, slot int) error {
	for {
		if err := ctx.Err(); err != nil {
			w.shuttingDown.Store(true)
			return nil
		}
		if err := w.repository.FailExhaustedBattles(ctx, w.config.MaxAttempts); err != nil {
			return fmt.Errorf("claim battle: %w", err)
		}
		job, err := w.repository.ClaimBattle(ctx, w.config.WorkerID, w.config.LeaseDuration, w.config.MaxAttempts)
		if errors.Is(err, queue.ErrNoBattleAvailable) {
			if !wait(ctx, w.config.PollInterval) {
				w.shuttingDown.Store(true)
				return nil
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("claim battle: %w", err)
		}
		w.processClaimedBattle(ctx, job, slot)
	}
}

func (w *Worker) processClaimedBattle(ctx context.Context, job *queue.Job, slot int) {
	metrics.ObserveEngineClaim(job.Lease.Attempt > 1)
	defer metrics.TrackEngineBattle()()
	jobContext, span := tracing.StartBattleProcess(ctx, job.Traceparent, job.Tracestate,
		attribute.String("battlefield.battle_id", job.Lease.BattleID),
		attribute.Int("battlefield.attempt", job.Lease.Attempt),
	)
	defer span.End()
	w.logger.InfoContext(jobContext, "battle_claimed",
		"slot", slot,
		"battle_id", job.Lease.BattleID,
		"attempt", job.Lease.Attempt,
		"reclaimed", job.Lease.Attempt > 1,
		"scenario_id", job.Scenario.ID,
		"requested_ticks", job.RequestedTicks,
	)
	processErr := w.process(jobContext, job)
	if processErr == nil {
		return
	}
	tracing.RecordError(span, processErr)
	attributes := []any{"battle_id", job.Lease.BattleID, "attempt", job.Lease.Attempt, "error", processErr}
	switch {
	case errors.Is(processErr, context.Canceled):
		// Graceful requeue paths log their successful handoff directly.
	case errors.Is(processErr, queue.ErrLeaseLost):
		w.logger.WarnContext(jobContext, "battle_lease_lost", attributes...)
	default:
		w.logger.ErrorContext(jobContext, "battle_processing_stopped", attributes...)
	}
}

func (w *Worker) process(ctx context.Context, job *queue.Job) (resultErr error) {
	started := time.Now()
	ctx = context.WithValue(ctx, battleStartedKey{}, started)
	outcome := "error"
	defer func() {
		if errors.Is(resultErr, queue.ErrLeaseLost) {
			outcome = "lease_lost"
		}
		metrics.ObserveEngineBattle(outcome, time.Since(started))
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

func (w *Worker) runBattle(ctx context.Context, job *queue.Job, simulation *game.Match) (string, error) {
	var progress progressLog
	for {
		outcome, finished, err := w.runTick(ctx, job.Lease, simulation)
		if finished {
			return outcome, err
		}
		if ctx.Err() == nil && progress.due(time.Now()) {
			w.logger.InfoContext(ctx, "battle_progress", "battle_id", job.Lease.BattleID,
				"attempt", job.Lease.Attempt, "current_tick", simulation.State().Tick,
				"requested_ticks", job.RequestedTicks, "elapsed_ms", elapsedBattleMilliseconds(ctx))
		}
	}
}

func (w *Worker) runTick(ctx context.Context, lease queue.Lease, simulation *game.Match) (string, bool, error) {
	if ctx.Err() != nil {
		outcome, err := requeueOutcome(w.requeueAfterCancellation(ctx, lease))
		return outcome, true, err
	}
	if _, err := w.waitWhilePaused(ctx, lease); err != nil {
		return pausedTickResult(err)
	}
	tickStarted := time.Now()
	transition, err := w.advanceSimulation(ctx, lease, simulation)
	if err != nil && ctx.Err() != nil {
		// Shutdown, or a sibling slot's failure, interrupted a decision window.
		outcome, err := requeueOutcome(w.requeueAfterCancellation(ctx, lease))
		return outcome, true, err
	}
	if err != nil {
		return "error", true, err
	}
	pausedFor, err := w.waitWhilePaused(ctx, lease)
	if err != nil {
		return pausedTickResult(err)
	}
	tickStarted = tickStarted.Add(pausedFor)
	metrics.ObserveEngineTick()
	if transition.Finished {
		outcome, err := w.completeBattle(ctx, lease, transition)
		return outcome, true, err
	}
	return w.persistRunningTick(ctx, lease, simulation, transition, tickStarted)
}

func (w *Worker) advanceSimulation(ctx context.Context, lease queue.Lease, simulation *game.Match) (game.Transition, error) {
	if !simulation.DecisionWindowDue() || w.decisions == nil {
		return simulation.Advance()
	}
	plans, err := w.resolveDecisionWindow(ctx, lease, simulation)
	if err != nil {
		return game.Transition{}, err
	}
	return simulation.AdvanceWithPlans(plans)
}

func (w *Worker) persistRunningTick(ctx context.Context, lease queue.Lease, simulation *game.Match, transition game.Transition, tickStarted time.Time) (string, bool, error) {
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
	w.logger.DebugContext(ctx, "battle_tick_completed", "battle_id", lease.BattleID, "attempt", lease.Attempt, "current_tick", transition.Tick, "duration_ms", time.Since(tickStarted).Milliseconds())
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

func (w *Worker) failInvalidScenario(ctx context.Context, lease queue.Lease, scenarioErr error) error {
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

func (w *Worker) completeBattle(ctx context.Context, lease queue.Lease, transition game.Transition) (string, error) {
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
		"elapsed_ms", elapsedBattleMilliseconds(ctx),
		"winner_faction_id", winner,
		"reason", transition.Result.Reason,
	)
	return "completed", nil
}
