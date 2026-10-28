package agentview

import (
	"bytes"
	"encoding/json"
	"sort"
)

// Order model-facing JSON without changing its schema or any value. Unknown fields
// stay in the dynamic portion; request metadata is always last. RawMessage avoids
// rounding large integers and preserves absent/null/empty distinctions.
// The object must be a fresh projection map; nested entries are reordered in place.
func orderedContext(object map[string]json.RawMessage) json.RawMessage {
	for key, order := range map[string][]string{
		"legal": {"actionKinds", "units", "buildings", "upgrades", "recruitmentSources", "economySites", "constructionRuns", "goldMineSites"},
		"view":  {"map", "unitProfiles", "buildingProfiles", "unitMap", "state"},
	} {
		if child, ok := jsonObject(object[key]); ok {
			object[key] = orderedObject(child, order, nil)
		}
	}
	return orderedObject(object, []string{
		"schemaVersion", "encoding", "continuousProduction", "knowledge",
		"factionName", "background", "doctrine", "battleId", "factionId", "ownedUpgradeIds",
		"legal", "view", "economy", "activePlan", "memory", "referenceUnits",
	}, []string{"opening", "requestId", "targetTick", "expectedVersion", "observationVersion", "observationHash"})
}

func orderedObject(object map[string]json.RawMessage, first, last []string) json.RawMessage {
	var out bytes.Buffer
	out.WriteByte('{')
	written := map[string]bool{}
	appendKey := func(key string) {
		value, exists := object[key]
		if !exists || written[key] {
			return
		}
		if len(written) > 0 {
			out.WriteByte(',')
		}
		out.Write(contextJSON(key))
		out.WriteByte(':')
		out.Write(value)
		written[key] = true
	}
	for _, key := range first {
		appendKey(key)
	}
	deferred := map[string]bool{}
	for _, key := range last {
		deferred[key] = true
	}
	var rest []string
	for key := range object {
		if !written[key] && !deferred[key] {
			rest = append(rest, key)
		}
	}
	sort.Strings(rest)
	for _, key := range append(rest, last...) {
		appendKey(key)
	}
	out.WriteByte('}')
	return out.Bytes()
}

func jsonObject(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	var object map[string]json.RawMessage
	err := json.Unmarshal(raw, &object)
	return object, err == nil && object != nil
}

// ContextBlocks partitions one prefetched envelope into disjoint reference and
// state objects. Recursively merging the blocks reconstructs the input exactly.
// Extract only known stable leaves: legal sites, observed profiles, visible-only
// reference units and future fields can change and must remain in state.
// With no reference leaves, callers should use the original single-block layout.
func ContextBlocks(payload json.RawMessage) (reference, state json.RawMessage, ok bool) {
	envelope, ok := jsonObject(payload)
	if !ok {
		return nil, nil, false
	}
	observation, ok := jsonObject(envelope["context"])
	if !ok {
		return nil, nil, false
	}
	stable := map[string]json.RawMessage{}
	moveContextFields(observation, stable, []string{"schemaVersion", "encoding", "continuousProduction", "knowledge"})
	for key, leaves := range map[string][]string{
		"legal": {"actionKinds", "units", "buildings", "upgrades"},
		"view":  {"map"},
	} {
		child, valid := jsonObject(observation[key])
		if !valid {
			continue
		}
		selected := map[string]json.RawMessage{}
		moveContextFields(child, selected, leaves)
		if len(selected) == 0 {
			continue
		}
		stable[key] = contextJSON(selected)
		if len(child) == 0 {
			delete(observation, key)
		} else {
			observation[key] = contextJSON(child)
		}
	}
	if len(stable) == 0 {
		return nil, nil, false
	}
	envelope["context"] = orderedContext(observation)
	return contextJSON(map[string]json.RawMessage{"context": orderedContext(stable)}), contextJSON(envelope), true
}

func moveContextFields(from, to map[string]json.RawMessage, keys []string) {
	for _, key := range keys {
		if value, exists := from[key]; exists {
			to[key] = value
			delete(from, key)
		}
	}
}
