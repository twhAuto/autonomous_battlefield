// Builds bounded summaries of earlier decisions from the same battle attempt and faction
// for later model context.

package observation

import (
	"encoding/json"
	"sort"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
)

const FactionMemoryLimit = 3
const FactionMemoryMaxBytes = 8192

// BuildFactionMemory checks scope again for alternate repository implementations.
// The SQL reader must apply scope and the row limit before retrieving records.
func BuildFactionMemory(records []ai.DecisionRecord, battleID string, attempt int, factionID string, beforeTick uint64) []ai.FactionMemoryEntry {
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
