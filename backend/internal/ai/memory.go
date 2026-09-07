// Builds bounded summaries of earlier decisions from the same battle attempt and faction
// for later model context.

package ai

import (
	"encoding/json"
	"sort"

	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
)

const FactionMemoryLimit = 3
const FactionMemoryMaxBytes = 8192

// FactionMemoryEntry contains only historical facts from a faction's authorized
// observation and its resolved plan. It never carries nested memory or raw text
// from providers. Status distinguishes accepted plans from engine fallbacks.
type FactionMemoryEntry struct {
	ObservationTick   uint64                  `json:"observationTick"`
	TargetTick        uint64                  `json:"targetTick"`
	Status            string                  `json:"status"`
	Economy           EconomyContext          `json:"economy"`
	OwnUnits          int                     `json:"ownUnits"`
	VisibleEnemyUnits int                     `json:"visibleEnemyUnits"`
	Plan              controller.PlanProposal `json:"plan"`
}

// BuildFactionMemory checks scope again for alternate repository implementations.
// The SQL reader must apply scope and the row limit before retrieving records.
func BuildFactionMemory(records []DecisionRecord, battleID string, attempt int, factionID string, beforeTick uint64) []FactionMemoryEntry {
	var entries []FactionMemoryEntry
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
	var bounded []FactionMemoryEntry
	for _, entry := range entries {
		candidate := append(append([]FactionMemoryEntry(nil), bounded...), entry)
		payload, err := json.Marshal(candidate)
		if err == nil && len(payload) <= FactionMemoryMaxBytes {
			bounded = candidate
		}
	}
	// Present observations chronologically, oldest first.
	sort.Slice(bounded, func(i, j int) bool { return bounded[i].TargetTick < bounded[j].TargetTick })
	return bounded
}

func summarizeFactionDecision(record DecisionRecord) FactionMemoryEntry {
	entry := FactionMemoryEntry{ObservationTick: record.Observation.View.State.Tick, TargetTick: record.TargetTick, Status: record.Status, Economy: record.Observation.Economy, Plan: *record.ResolvedPlan}
	for _, unit := range record.Observation.View.State.Units {
		if unit.FactionID == record.FactionID {
			entry.OwnUnits++
		} else {
			entry.VisibleEnemyUnits++
		}
	}
	return entry
}
