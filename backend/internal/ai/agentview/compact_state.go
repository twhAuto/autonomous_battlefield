package agentview

import (
	"encoding/json"
	"math"
	"sort"
)

// Model-only encodings never change canonical storage, hashes or submissions.
func compactDecisionData(original map[string]json.RawMessage) map[string]json.RawMessage {
	if hasCompactMarkers(original) {
		return original
	}
	result := contextObject(original)
	compactLegal(result)
	compactUnitMap(result)
	compactMemory(result)
	result["encoding"] = contextJSON("tables-v1")
	if len(contextJSON(result)) >= len(contextJSON(original)) {
		return original
	}
	return result
}

func compactLegal(context map[string]json.RawMessage) {
	legal, ok := jsonObject(context["legal"])
	if !ok {
		return
	}
	for _, key := range []string{"units", "buildings"} {
		if value, exists := legal[key]; exists {
			legal[key] = compactTable(value)
		}
	}
	if value, exists := legal["upgrades"]; exists {
		legal["upgrades"] = compactUpgrades(value)
	}
	compactSites(legal)
	context["legal"] = contextJSON(legal)
}

func compactUnitMap(context map[string]json.RawMessage) {
	view, ok := jsonObject(context["view"])
	if !ok || view["unitMap"] != nil {
		return
	}
	state, ok := jsonObject(view["state"])
	if !ok {
		return
	}
	var factions []map[string]json.RawMessage
	if json.Unmarshal(state["factions"], &factions) != nil {
		return
	}
	rows, valid := collectMapUnits(factions)
	if !valid || len(rows) == 0 {
		return
	}
	view["unitMap"] = contextJSON(tableFor(rows))
	state["factions"] = contextJSON(factions)
	view["state"] = contextJSON(state)
	candidate := contextJSON(view)
	if len(candidate) < len(context["view"]) {
		context["view"] = candidate
	}
}

func collectMapUnits(factions []map[string]json.RawMessage) ([]map[string]json.RawMessage, bool) {
	rows := []map[string]json.RawMessage{}
	for index, faction := range factions {
		raw, exists := faction["units"]
		if !exists {
			continue
		}
		units, valid := records(raw)
		if !valid || !mapUnitFields(units, index) {
			return nil, false
		}
		rows = append(rows, units...)
		delete(faction, "units")
	}
	return rows, true
}

func mapUnitFields(units []map[string]json.RawMessage, faction int) bool {
	for _, unit := range units {
		if unit["faction"] != nil || unit["xy"] != nil {
			return false
		}
		position, ok := jsonObject(unit["position"])
		if !ok {
			return false
		}
		x, y, valid := coordinate(position)
		if !valid {
			return false
		}
		unit["faction"] = contextJSON(faction)
		unit["xy"] = contextJSON([2]int64{x, y})
		delete(unit, "position")
	}
	return true
}

func coordinate(position map[string]json.RawMessage) (int64, int64, bool) {
	if len(position) != 2 || string(position["x"]) == "null" || string(position["y"]) == "null" {
		return 0, 0, false
	}
	var x, y int64
	if json.Unmarshal(position["x"], &x) != nil || json.Unmarshal(position["y"], &y) != nil {
		return 0, 0, false
	}
	return x, y, true
}

// Runs preserve source order, gaps and duplicates; endpoints are inclusive.
func compactSites(legal map[string]json.RawMessage) {
	if legal["constructionRuns"] != nil {
		return
	}
	var sites []map[string]json.RawMessage
	if json.Unmarshal(legal["economySites"], &sites) != nil || len(sites) == 0 {
		return
	}
	runs, ok := siteRuns(sites)
	if !ok {
		return
	}
	candidate := contextJSON(runs)
	if len(candidate)+len("constructionRuns") < len(legal["economySites"])+len("economySites") {
		legal["constructionRuns"] = candidate
		delete(legal, "economySites")
	}
}

func siteRuns(sites []map[string]json.RawMessage) ([][3]int64, bool) {
	runs := [][3]int64{}
	for _, site := range sites {
		x, y, valid := coordinate(site)
		if !valid {
			return nil, false
		}
		last := len(runs) - 1
		if last >= 0 && runs[last][0] == y && runs[last][2] < math.MaxInt64 && x == runs[last][2]+1 {
			runs[last][2] = x
		} else {
			runs = append(runs, [3]int64{y, x, x})
		}
	}
	return runs, true
}

func compactMemory(context map[string]json.RawMessage) {
	if context["memoryEconomyShared"] != nil {
		return
	}
	entries, ok := records(context["memory"])
	if !ok {
		return
	}
	active, _ := jsonObject(context["activePlan"])
	for _, entry := range entries {
		compactMemoryPlan(entry, active)
	}
	context["memory"] = contextJSON(entries)
	economies, valid := memoryEconomies(entries)
	if !valid {
		return
	}
	shared := sharedFields(economies)
	for i, economy := range economies {
		for key := range shared {
			delete(economy, key)
		}
		entries[i]["economy"] = contextJSON(economy)
	}
	after := contextJSON(entries)
	if len(after)+len(contextJSON(shared))+len(`,"memoryEconomyShared":`) < len(context["memory"]) {
		context["memoryEconomyShared"] = contextJSON(shared)
		context["memory"] = after
	}
}

func memoryEconomies(entries []map[string]json.RawMessage) ([]map[string]json.RawMessage, bool) {
	economies := make([]map[string]json.RawMessage, 0, len(entries))
	for _, entry := range entries {
		economy, ok := jsonObject(entry["economy"])
		if !ok {
			return nil, false
		}
		economies = append(economies, economy)
	}
	return economies, true
}

func compactMemoryPlan(entry, active map[string]json.RawMessage) {
	if entry["planFieldsFromActive"] != nil {
		return
	}
	plan, ok := jsonObject(entry["plan"])
	if !ok {
		return
	}
	refs := []string{}
	for key, value := range plan {
		if sameContextJSON(value, active[key]) && len(contextJSON(key))+1 < len(value) {
			refs = append(refs, key)
		}
	}
	if len(refs) == 0 {
		return
	}
	sort.Strings(refs)
	candidate := contextObject(entry)
	for _, key := range refs {
		delete(plan, key)
	}
	candidate["plan"] = contextJSON(plan)
	candidate["planFieldsFromActive"] = contextJSON(refs)
	if len(contextJSON(candidate)) < len(contextJSON(entry)) {
		entry["plan"] = candidate["plan"]
		entry["planFieldsFromActive"] = candidate["planFieldsFromActive"]
	}
}

// Reserved names in future/unknown input must not be reinterpreted as our format.
func hasCompactMarkers(context map[string]json.RawMessage) bool {
	if context["encoding"] != nil || context["memoryEconomyShared"] != nil {
		return true
	}
	legal, _ := jsonObject(context["legal"])
	view, _ := jsonObject(context["view"])
	if legal["constructionRuns"] != nil || view["unitMap"] != nil {
		return true
	}
	var entries []map[string]json.RawMessage
	_ = json.Unmarshal(context["memory"], &entries)
	for _, entry := range entries {
		if entry["planFieldsFromActive"] != nil {
			return true
		}
	}
	return false
}
