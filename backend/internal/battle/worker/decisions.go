package worker

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
	"github.com/twh09/autonomous_battlefield/backend/internal/telemetry"
	"log/slog"
	"time"
)

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
	decisionContext.Knowledge, err = ai.RetrieveKnowledge(simulation.Scenario().Ruleset, decisionContext, w.config.KnowledgeRetrieval)
	if err != nil {
		return err
	}
	decisionContext.Memory, err = w.decisions.ListFactionMemory(ctx, lease.BattleID, lease.Attempt, factionID, targetTick)
	if err != nil {
		// Memory is optional continuity. Current authoritative context remains usable.
		w.logger.WarnContext(ctx, "faction_memory_unavailable", "battle_id", lease.BattleID, "attempt", lease.Attempt, "faction_id", factionID)
		decisionContext.Memory = nil
	}
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
	return ai.DecisionContext{Doctrine: snapshot.Doctrine, FactionName: snapshot.FactionName, Background: snapshot.Background, Opening: snapshot.Opening, OwnedUpgradeIDs: snapshot.OwnedUpgradeIDs, SchemaVersion: ai.ContextSchema, RequestID: decisionID(lease.BattleID, lease.Attempt, factionID, targetTick), BattleID: lease.BattleID,
		FactionID: factionID, TargetTick: targetTick, ExpectedVersion: 1, ObservationVersion: snapshot.Version,
		View: snapshot.View, Economy: ai.EconomyContext{Gold: snapshot.Gold, ProjectedIncome: snapshot.ProjectedIncome,
			EconomyBuildingCost: snapshot.EconomyCost, EconomyBuildingIncome: snapshot.EconomyIncome,
			CurrentEconomyBuildings: snapshot.EconomyCount, MaximumEconomyBuildings: snapshot.MaximumEconomy},
		Legal: ai.LegalChoices{Upgrades: snapshot.LegalUpgrades, ActionKinds: []string{game.ActionAttack, game.ActionDefend}, Units: snapshot.LegalUnits, EconomySites: snapshot.LegalSites}, ActivePlan: snapshot.ActivePlan}
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
	var progress progressLog
	started := time.Now()
	for {
		pausedFor, err := w.waitWhilePaused(ctx, lease)
		if err != nil {
			return err
		}
		deadline = deadline.Add(pausedFor)
		if pausedFor > 0 {
			nextHeartbeat = time.Now().Add(w.config.HeartbeatInterval)
		}
		records, err := w.decisions.ListWindowDecisions(ctx, lease.BattleID, lease.Attempt, targetTick)
		if err != nil {
			return err
		}
		pending := pendingDecisionCount(records)
		if pending == 0 || !time.Now().Before(deadline) {
			return nil
		}
		if progress.due(time.Now()) {
			w.logger.InfoContext(ctx, "decision_window_waiting", "battle_id", lease.BattleID,
				"attempt", lease.Attempt, "target_tick", targetTick, "pending_count", pending,
				"elapsed_ms", time.Since(started).Milliseconds(), "remaining_ms", max(0, time.Until(deadline).Milliseconds()))
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

func pendingDecisionCount(records []ai.DecisionRecord) int {
	count := 0
	for _, record := range records {
		if record.Status == "pending" || record.Status == "running" {
			count++
		}
	}
	return count
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
		category := fallbackCategory(record)
		accepted, err := w.decisions.ResolveDecisionFallback(ctx, record.ID, record.Version, fallback, source, category)
		if err != nil {
			return record, err
		}
		if accepted {
			resolvedAt := time.Now()
			record.Status, record.ResolvedPlan, record.FallbackSource = "fallback", &fallback, source
			record.ErrorCategory, record.ResolvedAt = category, &resolvedAt
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
	if targetTick == 0 || targetTick == uint64(simulation.Scenario().DecisionIntervalTicks) {
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

func fallbackCategory(record ai.DecisionRecord) string {
	if record.Status == "failed" && record.ErrorCategory != "" {
		return record.ErrorCategory
	}
	return "timeout"
}
