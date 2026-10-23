package agentview

import "encoding/json"

// unitReferenceFields name unit and building fields that may refer to a unit ID.
var unitReferenceFields = []string{"attackTargetId", "healTargetId"}

// observedFactions holds one decoded faction group with its entity lists.
type observedFactions struct {
	groups    []map[string]json.RawMessage
	units     [][]map[string]json.RawMessage
	buildings [][]map[string]json.RawMessage
	deaths    []map[string]json.RawMessage
	index     map[string]int
}

// compactUnitIDs gives every observed unit, living or recently destroyed, a
// context-local integer ID and rewrites unit references to it. Building IDs stay
// complete because submissions name them. Destroyed buildings no longer block,
// count toward limits or act, so they are omitted; faction stats still count them.
// Unexpected shapes, including already-numeric IDs, leave the context unchanged.
func compactUnitIDs(context map[string]json.RawMessage) map[string]json.RawMessage {
	view, ok := jsonObject(context["view"])
	if !ok {
		return context
	}
	state, ok := jsonObject(view["state"])
	if !ok {
		return context
	}
	observed, ok := decodeObservedFactions(state)
	if !ok {
		return context
	}
	ids, ok := assignUnitIDs(append(append([][]map[string]json.RawMessage{}, observed.units...), observed.deaths))
	if !ok {
		return context
	}
	for i, faction := range observed.groups {
		encodeFactionEntities(faction, observed.units[i], observed.buildings[i], ids)
	}
	indexDeathFactions(observed.deaths, observed.index)
	state["factions"] = contextJSON(observed.groups)
	if state["unitDeaths"] != nil {
		state["unitDeaths"] = contextJSON(observed.deaths)
	}
	view["state"] = contextJSON(state)
	context["view"] = contextJSON(view)
	return context
}

func decodeObservedFactions(state map[string]json.RawMessage) (observedFactions, bool) {
	groups, ok := entityList(state["factions"])
	if !ok {
		return observedFactions{}, false
	}
	deaths, ok := entityList(state["unitDeaths"])
	if !ok {
		return observedFactions{}, false
	}
	observed := observedFactions{groups: groups, deaths: deaths, index: map[string]int{},
		units: make([][]map[string]json.RawMessage, len(groups)), buildings: make([][]map[string]json.RawMessage, len(groups))}
	for i, faction := range groups {
		var factionID string
		_ = json.Unmarshal(faction["factionId"], &factionID)
		observed.index[factionID] = i
		units, unitsOK := entityList(faction["units"])
		buildings, buildingsOK := entityList(faction["buildings"])
		if !unitsOK || !buildingsOK {
			return observedFactions{}, false
		}
		observed.units[i], observed.buildings[i] = units, buildings
	}
	return observed, true
}

// assignUnitIDs numbers units in list order from 1, replacing each string ID.
func assignUnitIDs(lists [][]map[string]json.RawMessage) (map[string]int, bool) {
	ids := map[string]int{}
	for _, list := range lists {
		for _, unit := range list {
			var id string
			if json.Unmarshal(unit["id"], &id) != nil || id == "" || ids[id] != 0 {
				return nil, false
			}
			ids[id] = len(ids) + 1
		}
	}
	for _, list := range lists {
		for _, unit := range list {
			var id string
			_ = json.Unmarshal(unit["id"], &id)
			unit["id"] = contextJSON(ids[id])
		}
	}
	return ids, true
}

func encodeFactionEntities(faction map[string]json.RawMessage, units, buildings []map[string]json.RawMessage, ids map[string]int) {
	if faction["units"] != nil {
		rewriteUnitReferences(units, ids)
		faction["units"] = contextJSON(units)
	}
	living := livingBuildings(buildings)
	if len(living) == 0 {
		delete(faction, "buildings")
		return
	}
	rewriteUnitReferences(living, ids)
	faction["buildings"] = contextJSON(living)
}

// indexDeathFactions replaces a known faction ID with its faction group index.
func indexDeathFactions(deaths []map[string]json.RawMessage, index map[string]int) {
	for _, death := range deaths {
		var factionID string
		if json.Unmarshal(death["factionId"], &factionID) != nil {
			continue
		}
		if position, known := index[factionID]; known {
			death["faction"] = contextJSON(position)
			delete(death, "factionId")
		}
	}
}

// entityList treats an absent list as empty; null entries are invalid.
func entityList(raw json.RawMessage) ([]map[string]json.RawMessage, bool) {
	if raw == nil {
		return nil, true
	}
	var entities []map[string]json.RawMessage
	if json.Unmarshal(raw, &entities) != nil {
		return nil, false
	}
	for _, entity := range entities {
		if entity == nil {
			return nil, false
		}
	}
	return entities, true
}

func rewriteUnitReferences(entities []map[string]json.RawMessage, ids map[string]int) {
	for _, entity := range entities {
		for _, field := range unitReferenceFields {
			var target string
			if json.Unmarshal(entity[field], &target) != nil {
				continue
			}
			if id, known := ids[target]; known {
				entity[field] = contextJSON(id)
			}
		}
	}
}

func livingBuildings(buildings []map[string]json.RawMessage) []map[string]json.RawMessage {
	living := make([]map[string]json.RawMessage, 0, len(buildings))
	for _, building := range buildings {
		var health int64
		if json.Unmarshal(building["health"], &health) == nil && health <= 0 {
			continue
		}
		living = append(living, building)
	}
	return living
}
