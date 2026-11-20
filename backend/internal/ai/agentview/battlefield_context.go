package agentview

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/game"
)

// Only the agent projection changes; persisted observations retain all fields.
type battlefieldContext struct {
	Context
	View map[string]json.RawMessage `json:"view"`
}

func contextJSON(value any) json.RawMessage {
	// All callers use JSON-safe observation structs or their JSON projections.
	payload, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return payload
}

func contextObject(value any) map[string]json.RawMessage {
	var result map[string]json.RawMessage
	if err := json.Unmarshal(contextJSON(value), &result); err != nil {
		panic(err)
	}
	return result
}

// Keep unknown/new unit fields on the individual observation by default.
var effectiveProfileFields = []string{"type", "class", "maxHealth", "attack", "defense", "speed", "movement", "range", "vision", "cost", "attackMode", "minimumRange", "buildingPriority", "buildingDamageBonus", "bonusAgainstClass", "classDamageBonus", "healAmount", "healRange", "scout", "preferredTargetClass", "armorPiercing", "repairAmount", "repairCost"}

type visibleRow struct {
	Y       int      `json:"y"`
	XRanges [][2]int `json:"xRanges"`
}

func compactBattlefieldView(view game.View) map[string]json.RawMessage {
	result := contextObject(view)
	state := contextObject(view.State)
	units := make([]map[string]json.RawMessage, 0, len(view.State.Units))
	profiles := map[string]map[string]json.RawMessage{}
	profileIDs := map[string]string{}
	for _, unit := range view.State.Units {
		item := contextObject(unit)
		if unit.RepairAmount > 0 {
			item["repairCharges"] = contextJSON(unit.RepairCharges)
		}
		profile := map[string]json.RawMessage{}
		for _, key := range effectiveProfileFields {
			if value, exists := item[key]; exists {
				profile[key] = value
				delete(item, key)
			}
		}
		key := string(contextJSON(profile))
		id, exists := profileIDs[key]
		if !exists {
			id = fmt.Sprintf("p%d", len(profiles)+1)
			profileIDs[key] = id
			profiles[id] = profile
		}
		item["profile"] = contextJSON(id)
		units = append(units, item)
	}
	state["units"] = contextJSON(units)
	withProfiles := contextObject(view)
	withProfiles["state"] = contextJSON(state)
	withProfiles["unitProfiles"] = contextJSON(profiles)
	// Unique profiles and tiny battles may cost more than inline stats.
	if len(contextJSON(withProfiles)) < len(contextJSON(result)) {
		result = withProfiles
	}

	if len(view.VisiblePositions) == view.Map.Width*view.Map.Height {
		delete(result, "visiblePositions")
		return result
	}
	rows := compactVisibleRows(view.VisiblePositions)
	encoded := contextJSON(rows)
	if len(encoded)+len("visibleRowRanges") < len(result["visiblePositions"])+len("visiblePositions") {
		delete(result, "visiblePositions")
		result["visibleRowRanges"] = encoded
	}
	return result
}

func compactVisibleRows(visible []game.Position) []visibleRow {
	positions := append([]game.Position(nil), visible...)
	sort.Slice(positions, func(i, j int) bool {
		if positions[i].Y != positions[j].Y {
			return positions[i].Y < positions[j].Y
		}
		return positions[i].X < positions[j].X
	})
	rows := []visibleRow{}
	for _, position := range positions {
		if len(rows) == 0 || rows[len(rows)-1].Y != position.Y {
			rows = append(rows, visibleRow{Y: position.Y})
		}
		row := &rows[len(rows)-1]
		if len(row.XRanges) > 0 && position.X <= row.XRanges[len(row.XRanges)-1][1]+1 {
			row.XRanges[len(row.XRanges)-1][1] = position.X
		} else {
			row.XRanges = append(row.XRanges, [2]int{position.X, position.X})
		}
	}
	return rows
}
