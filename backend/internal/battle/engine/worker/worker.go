// Package worker orchestrates durable queued battles around the pure engine.
package worker

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/battle/simulation/controller"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
)

type Config struct {
	WorkerID          string
	PollInterval      time.Duration
	LeaseDuration     time.Duration
	HeartbeatInterval time.Duration
	ShutdownTimeout   time.Duration
	MaxAttempts       int
	DecisionTimeout   time.Duration
	CapabilitySecret  string
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

func pausedTickResult(err error) (string, bool, error) {
	if errors.Is(err, context.Canceled) {
		return "", false, nil
	}
	return "error", true, err
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

func (w *Worker) resolveDecisionWindow(ctx context.Context, lease BattleLease, simulation *game.Match) (map[string]controller.PlanProposal, error) {
	targetTick := simulation.DecisionTargetTick()
	baselines := simulation.ScriptedPlans()
	service := ai.NewDecisionService(w.decisions)
	w.logger.InfoContext(ctx, "decision_window_started",
		"battle_id", lease.BattleID,
		"attempt", lease.Attempt,
		"target_tick", targetTick,
		"participant_count", len(simulation.FactionIDs()),
		"decision_timeout_ms", w.config.DecisionTimeout.Milliseconds(),
	)
	if err := w.prepareDecisionWindow(ctx, lease, simulation, targetTick, baselines, service); err != nil {
		return nil, err
	}

	if err := w.waitForWindowDecisions(ctx, lease, targetTick); err != nil {
		return nil, err
	}

	records, err := w.decisions.ListWindowDecisions(ctx, lease.BattleID, lease.Attempt, targetTick)
	if err != nil {
		return nil, err
	}
	plans, acceptedCount, fallbackCount, err := w.resolveDecisionPlans(ctx, lease, simulation, targetTick, baselines, records)
	if err != nil {
		return nil, err
	}
	w.logger.InfoContext(ctx, "decision_window_completed",
		"battle_id", lease.BattleID,
		"attempt", lease.Attempt,
		"target_tick", targetTick,
		"accepted_count", acceptedCount,
		"fallback_count", fallbackCount,
	)
	return plans, nil
}

func (w *Worker) prepareDecisionWindow(ctx context.Context, lease BattleLease, simulation *game.Match, targetTick uint64, baselines map[string]controller.PlanProposal, service *ai.DecisionService) error {
	for _, factionID := range simulation.FactionIDs() {
		if err := w.prepareDecision(ctx, lease, simulation, factionID, targetTick, baselines, service); err != nil {
			return err
		}
	}
	return nil
}

func (w *Worker) prepareDecision(ctx context.Context, lease BattleLease, simulation *game.Match, factionID string, targetTick uint64, baselines map[string]controller.PlanProposal, service *ai.DecisionService) error {
	snapshot, err := simulation.DecisionSnapshot(factionID)
	if err != nil {
		return err
	}
	setup := factionSetup(simulation.Scenario(), factionID)
	decisionContext := newDecisionContext(lease, factionID, targetTick, snapshot)
	hash, err := ai.ObservationHash(decisionContext)
	if err != nil {
		return err
	}
	decisionContext.ObservationHash = hash
	traceparent, tracestate := telemetry.TraceContext(ctx)
	record, err := w.decisions.CreateOrReuseDecision(ctx, ai.CreateDecisionParams{ID: decisionContext.RequestID, BattleID: lease.BattleID, Attempt: lease.Attempt,
		FactionID: factionID, TargetTick: targetTick, ControllerType: setup.Controller.Type, ProfileID: setup.Controller.ProfileID,
		Provider: setup.Controller.Provider, Runtime: setup.Controller.Runtime, ModelIdentifier: setup.Controller.ModelIdentifier,
		PromptVersion: ai.PromptVersion, ToolSchemaVersion: ai.ToolSchema, Observation: decisionContext, ObservationHash: hash,
		Traceparent: traceparent, Tracestate: tracestate})
	if err != nil {
		return err
	}
	w.logPreparedDecision(ctx, lease, targetTick, factionID, setup, record)
	if setup.Controller.Type == ai.ControllerScripted && record.ResolvedPlan == nil {
		return submitScriptedPlan(ctx, service, record, targetTick, baselines[factionID])
	}
	return nil
}

func newDecisionContext(lease BattleLease, factionID string, targetTick uint64, snapshot game.DecisionSnapshot) ai.DecisionContext {
	return ai.DecisionContext{SchemaVersion: ai.ContextSchema, RequestID: decisionID(lease.BattleID, lease.Attempt, factionID, targetTick), BattleID: lease.BattleID,
		FactionID: factionID, TargetTick: targetTick, ExpectedVersion: 1, ObservationVersion: snapshot.Version,
		View: snapshot.View, Economy: ai.EconomyContext{Gold: snapshot.Gold, ProjectedIncome: snapshot.ProjectedIncome,
			EconomyBuildingCost: snapshot.EconomyCost, EconomyBuildingIncome: snapshot.EconomyIncome,
			CurrentEconomyBuildings: snapshot.EconomyCount, MaximumEconomyBuildings: snapshot.MaximumEconomy},
		Legal: ai.LegalChoices{ActionKinds: []string{game.ActionAttack, game.ActionDefend}, Units: snapshot.LegalUnits, EconomySites: snapshot.LegalSites}, ActivePlan: snapshot.ActivePlan}
}

func (w *Worker) logPreparedDecision(ctx context.Context, lease BattleLease, targetTick uint64, factionID string, setup game.FactionSetup, record ai.DecisionRecord) {
	w.logger.InfoContext(ctx, "decision_prepared", "battle_id", lease.BattleID, "attempt", lease.Attempt,
		"request_id", record.ID, "faction_id", factionID, "target_tick", targetTick, "controller_type", setup.Controller.Type,
		"profile_id", setup.Controller.ProfileID, "provider", setup.Controller.Provider, "runtime", setup.Controller.Runtime, "decision_status", record.Status)
}

func submitScriptedPlan(ctx context.Context, service *ai.DecisionService, record ai.DecisionRecord, targetTick uint64, plan controller.PlanProposal) error {
	result, err := service.SubmitPlan(ctx, ai.PlanSubmission{RequestID: record.ID, IdempotencyKey: record.ID + ":scripted", ExpectedVersion: record.Version, TargetTick: targetTick, Plan: plan})
	if err != nil {
		return err
	}
	if !result.Accepted {
		return fmt.Errorf("scripted plan rejected: %v", result.Errors)
	}
	return nil
}

func (w *Worker) waitForWindowDecisions(ctx context.Context, lease BattleLease, targetTick uint64) error {
	deadline := time.Now().Add(w.config.DecisionTimeout)
	nextHeartbeat := time.Now().Add(w.config.HeartbeatInterval)
	for {
		pausedFor, err := w.waitWhilePaused(ctx, lease)
		if err != nil {
			return err
		}
		deadline = deadline.Add(pausedFor)
		nextHeartbeat = time.Now().Add(w.config.HeartbeatInterval)
		records, err := w.decisions.ListWindowDecisions(ctx, lease.BattleID, lease.Attempt, targetTick)
		if err != nil {
			return err
		}
		if !decisionsPending(records) || !time.Now().Before(deadline) {
			return nil
		}
		if !wait(ctx, min(100*time.Millisecond, time.Until(deadline), time.Until(nextHeartbeat))) {
			return context.Canceled
		}
		if !time.Now().Before(nextHeartbeat) {
			if err := w.repository.HeartbeatBattle(ctx, lease, w.config.LeaseDuration); err != nil {
				return err
			}
			nextHeartbeat = time.Now().Add(w.config.HeartbeatInterval)
		}
	}
}

func decisionsPending(records []ai.DecisionRecord) bool {
	for _, record := range records {
		if record.Status == "pending" || record.Status == "running" {
			return true
		}
	}
	return false
}

func (w *Worker) resolveDecisionPlans(ctx context.Context, lease BattleLease, simulation *game.Match, targetTick uint64, baselines map[string]controller.PlanProposal, records []ai.DecisionRecord) (map[string]controller.PlanProposal, int, int, error) {
	plans := make(map[string]controller.PlanProposal, len(records))
	acceptedCount, fallbackCount := 0, 0
	for _, record := range records {
		resolved, err := w.resolveDecisionRecord(ctx, simulation, targetTick, baselines, record)
		if err != nil {
			return nil, 0, 0, err
		}
		if resolved.Status == "fallback" {
			fallbackCount++
		} else {
			acceptedCount++
		}
		w.logResolvedDecision(ctx, lease, resolved)
		plans[resolved.FactionID] = *resolved.ResolvedPlan
	}
	return plans, acceptedCount, fallbackCount, nil
}

func (w *Worker) resolveDecisionRecord(ctx context.Context, simulation *game.Match, targetTick uint64, baselines map[string]controller.PlanProposal, record ai.DecisionRecord) (ai.DecisionRecord, error) {
	if record.Status == "failed" && record.ErrorCategory == "battle_stopped" {
		return record, ErrBattleLeaseLost
	}
	if record.ResolvedPlan == nil {
		fallback, source := fallbackPlan(simulation, targetTick, baselines, record)
		accepted, err := w.decisions.ResolveDecisionFallback(ctx, record.ID, record.Version, fallback, source, "timeout")
		if err != nil {
			return record, err
		}
		if accepted {
			resolvedAt := time.Now()
			record.Status, record.ResolvedPlan, record.FallbackSource = "fallback", &fallback, source
			record.ErrorCategory, record.ResolvedAt = "timeout", &resolvedAt
		} else {
			record, err = w.decisions.GetDecision(ctx, record.ID)
			if err != nil {
				return record, err
			}
		}
	}
	if record.ResolvedPlan == nil {
		return record, fmt.Errorf("decision %s has no resolved plan", record.ID)
	}
	return record, nil
}

func fallbackPlan(simulation *game.Match, targetTick uint64, baselines map[string]controller.PlanProposal, record ai.DecisionRecord) (controller.PlanProposal, string) {
	if targetTick == uint64(simulation.Scenario().DecisionIntervalTicks) {
		return baselines[record.FactionID], "scripted_fallback"
	}
	return record.Observation.ActivePlan, "previous_plan"
}

func (w *Worker) logResolvedDecision(ctx context.Context, lease BattleLease, record ai.DecisionRecord) {
	level := slog.LevelInfo
	if record.Status == "fallback" || record.Status == "failed" {
		level = slog.LevelWarn
	}
	w.logger.LogAttrs(ctx, level, "decision_resolved", slog.String("battle_id", lease.BattleID), slog.Int("attempt", lease.Attempt),
		slog.String("request_id", record.ID), slog.String("faction_id", record.FactionID), slog.Uint64("target_tick", record.TargetTick),
		slog.String("controller_type", record.ControllerType), slog.String("profile_id", record.ProfileID), slog.String("provider", record.Provider),
		slog.String("runtime", record.Runtime), slog.String("decision_status", record.Status), slog.String("fallback_source", record.FallbackSource),
		slog.String("error_category", record.ErrorCategory), slog.Int64("duration_ms", decisionDurationMilliseconds(record)))
}

func decisionDurationMilliseconds(record ai.DecisionRecord) int64 {
	if record.ResolvedAt == nil || record.CreatedAt.IsZero() {
		return 0
	}
	return record.ResolvedAt.Sub(record.CreatedAt).Milliseconds()
}

func factionSetup(scenario game.Scenario, factionID string) game.FactionSetup {
	for _, setup := range scenario.Factions {
		if setup.ID == factionID {
			return setup
		}
	}
	return game.FactionSetup{}
}

func decisionID(battleID string, attempt int, factionID string, targetTick uint64) string {
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%s:%d", battleID, attempt, factionID, targetTick)))
	bytes := hash[:16]
	bytes[6] = (bytes[6] & 0x0f) | 0x50
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", bytes[0:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:16])
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
