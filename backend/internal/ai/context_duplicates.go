package ai

import (
	"bytes"
	"encoding/json"
)

// References point only into this frozen response, never another turn or live state.
func deduplicateContext(value any) map[string]json.RawMessage {
	result := contextObject(value)
	var memory []map[string]json.RawMessage
	_ = json.Unmarshal(result["memory"], &memory)
	for _, entry := range memory {
		if len(entry["plan"]) > len(contextJSON("#/activePlan"))+3 && sameContextJSON(entry["plan"], result["activePlan"]) {
			delete(entry, "plan")
			entry["planRef"] = contextJSON("#/activePlan")
		}
	}
	if len(memory) > 0 {
		result["memory"] = contextJSON(memory)
	}
	view := contextObject(result["view"])
	state := contextObject(view["state"])
	if sameContextJSON(state["battleId"], result["battleId"]) {
		delete(state, "battleId")
	}
	if sameContextJSON(state["tick"], result["observationVersion"]) {
		delete(state, "tick")
	}
	var factions []map[string]json.RawMessage
	_ = json.Unmarshal(state["factions"], &factions)
	economy := contextObject(result["economy"])
	for _, faction := range factions {
		if !sameContextJSON(faction["factionId"], result["factionId"]) {
			continue
		}
		if sameContextJSON(faction["gold"], economy["gold"]) {
			delete(faction, "gold")
		}
		if sameContextJSON(faction["ownedUpgradeIds"], result["ownedUpgradeIds"]) {
			delete(faction, "ownedUpgradeIds")
		}
	}
	if len(factions) > 0 {
		state["factions"] = contextJSON(factions)
	}
	view["state"] = contextJSON(state)
	result["view"] = contextJSON(view)
	return result
}

func sameContextJSON(a, b json.RawMessage) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	// Canonicalize objects so field ordering is irrelevant; retain numeric precision.
	canonical := func(raw json.RawMessage) []byte {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if decoder.Decode(&value) != nil {
			return nil
		}
		return contextJSON(value)
	}
	return bytes.Equal(canonical(a), canonical(b))
}
