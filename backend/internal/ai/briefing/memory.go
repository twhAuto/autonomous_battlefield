// Builds bounded summaries of earlier decisions from the same battle attempt and faction
// for later model context.

package briefing

import (
	"encoding/json"
	"sort"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
)

const FactionMemoryLimit = 3
const FactionMemoryMaxBytes = 8192

// OutcomeEvent is the subset of a recorded battle event that plan outcomes use.
type OutcomeEvent struct {
	Tick      uint64
	Type      string
	FactionID string
	Kind      string
	Reason    string
}

// BuildFactionMemory checks scope again for alternate repository implementations.
// The SQL reader must apply scope and the row limit before retrieving records.
// Events outside the remembered windows are ignored.
func BuildFactionMemory(records []ai.DecisionRecord, events []OutcomeEvent, battleID string, attempt int, factionID string, beforeTick uint64) []ai.FactionMemoryEntry {
	var entries []ai.FactionMemoryEntry
	for _, record := range records {
		if record.BattleID != battleID || record.Attempt != attempt || record.FactionID != factionID || record.TargetTick >= beforeTick || record.ResolvedPlan == nil || (record.Status != "accepted" && record.Status != "fallback") {
			continue
		}
		entry := summarizeFactionDecision(record)
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].TargetTick > entries[j].TargetTick })
	if len(entries) > FactionMemoryLimit {
		entries = entries[:FactionMemoryLimit]
	}
	attachPlanOutcomes(entries, events, factionID, beforeTick)
	// Prefer recent entries and omit whole oversized entries, never truncate JSON.
	var bounded []ai.FactionMemoryEntry
	for _, entry := range entries {
		candidate := append(append([]ai.FactionMemoryEntry(nil), bounded...), entry)
		payload, err := json.Marshal(candidate)
		if err == nil && len(payload) <= FactionMemoryMaxBytes {
			bounded = candidate
		}
	}
	// Present observations chronologically, oldest first.
	sort.Slice(bounded, func(i, j int) bool { return bounded[i].TargetTick < bounded[j].TargetTick })
	return bounded
}

// attachPlanOutcomes expects entries newest first; each window ends where the
// next newer plan, or the current decision, begins.
func attachPlanOutcomes(entries []ai.FactionMemoryEntry, events []OutcomeEvent, factionID string, beforeTick uint64) {
	end := beforeTick
	for i := range entries {
		outcome := planOutcome(events, factionID, entries[i].TargetTick, end)
		entries[i].Outcome = &outcome
		end = entries[i].TargetTick
	}
}

func planOutcome(events []OutcomeEvent, factionID string, from, until uint64) ai.PlanOutcome {
	outcome := ai.PlanOutcome{ThroughTick: until - 1}
	skipped := map[ai.SkippedOrders]int{}
	for _, event := range events {
		if event.Tick < from || event.Tick >= until {
			continue
		}
		own := event.FactionID == factionID
		switch {
		case event.Type == "unit_recruited" && own:
			outcome.UnitsTrained++
		case event.Type == "unit_destroyed" && own:
			outcome.UnitsLost++
		case event.Type == "unit_destroyed":
			outcome.EnemyUnitsDestroyed++
		case event.Type == "building_constructed" && own:
			outcome.BuildingsConstructed++
		case event.Type == "building_destroyed" && own:
			outcome.BuildingsLost++
		case event.Type == "building_destroyed":
			outcome.EnemyBuildingsDestroyed++
		case event.Type == "production_skipped" && own:
			skipped[ai.SkippedOrders{Kind: event.Kind, Reason: event.Reason}]++
		}
	}
	for key, count := range skipped {
		key.Count = count
		outcome.Skipped = append(outcome.Skipped, key)
	}
	sort.Slice(outcome.Skipped, func(i, j int) bool {
		a, b := outcome.Skipped[i], outcome.Skipped[j]
		return a.Kind < b.Kind || a.Kind == b.Kind && a.Reason < b.Reason
	})
	return outcome
}

func summarizeFactionDecision(record ai.DecisionRecord) ai.FactionMemoryEntry {
	entry := ai.FactionMemoryEntry{ObservationTick: record.Observation.View.State.Tick, TargetTick: record.TargetTick, Status: record.Status, Economy: record.Observation.Economy, Plan: *record.ResolvedPlan}
	for _, unit := range record.Observation.View.State.Units {
		if unit.FactionID == record.FactionID {
			entry.OwnUnits++
		} else {
			entry.VisibleEnemyUnits++
		}
	}
	return entry
}
