package agentview

import (
	"encoding/json"
	"fmt"
)

type factionObservationGroup struct {
	FactionID string                       `json:"factionId"`
	Stats     map[string]json.RawMessage   `json:"stats,omitempty"`
	Units     []map[string]json.RawMessage `json:"units,omitempty"`
	Buildings []map[string]json.RawMessage `json:"buildings,omitempty"`
}

// Group only saved visible entities. IDs and all individual observations remain
// unchanged; absent faction statistics are not synthesized from another source.
func groupContextFactions(result map[string]json.RawMessage) map[string]json.RawMessage {
	view := contextObject(result["view"])
	state := contextObject(view["state"])
	var factions, units, buildings []map[string]json.RawMessage
	_ = json.Unmarshal(state["factions"], &factions)
	_ = json.Unmarshal(state["units"], &units)
	_ = json.Unmarshal(state["buildings"], &buildings)
	buildings, profiles := buildingProfiles(buildings)
	if profiles != nil {
		view["buildingProfiles"] = contextJSON(profiles)
	}
	groups := []factionObservationGroup{}
	indices := map[string]int{}
	groupFor := func(raw json.RawMessage) int {
		var id string
		_ = json.Unmarshal(raw, &id)
		if index, ok := indices[id]; ok {
			return index
		}
		index := len(groups)
		indices[id] = index
		groups = append(groups, factionObservationGroup{FactionID: id})
		return index
	}
	for _, faction := range factions {
		index := groupFor(faction["factionId"])
		delete(faction, "factionId")
		groups[index].Stats = faction
	}
	for _, unit := range units {
		index := groupFor(unit["factionId"])
		delete(unit, "factionId")
		groups[index].Units = append(groups[index].Units, unit)
	}
	for _, building := range buildings {
		index := groupFor(building["factionId"])
		delete(building, "factionId")
		groups[index].Buildings = append(groups[index].Buildings, building)
	}
	state["factions"] = contextJSON(groups)
	delete(state, "units")
	delete(state, "buildings")
	view["state"] = contextJSON(state)
	result["view"] = contextJSON(view)
	return result
}

func buildingProfiles(buildings []map[string]json.RawMessage) ([]map[string]json.RawMessage, map[string]map[string]json.RawMessage) {
	items := make([]map[string]json.RawMessage, 0, len(buildings))
	profiles := map[string]map[string]json.RawMessage{}
	ids := map[string]string{}
	for _, building := range buildings {
		item := contextObject(building)
		profile := map[string]json.RawMessage{}
		for _, key := range []string{"type", "maxHealth", "defense", "vision", "cost", "income", "attack", "range"} {
			if value, ok := item[key]; ok {
				profile[key] = value
				delete(item, key)
			}
		}
		key := string(contextJSON(profile))
		id, ok := ids[key]
		if !ok {
			id = fmt.Sprintf("b%d", len(profiles)+1)
			ids[key] = id
			profiles[id] = profile
		}
		item["profile"] = contextJSON(id)
		items = append(items, item)
	}
	// Include the profile-map key and separator in the size comparison.
	if len(contextJSON(items))+len(contextJSON(profiles))+len(`,"buildingProfiles":`) < len(contextJSON(buildings)) {
		return items, profiles
	}
	return buildings, nil
}
