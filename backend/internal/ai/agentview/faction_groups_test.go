package agentview

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/game"
)

func TestFactionGroupingPreservesEveryEntity(t *testing.T) {
	state := game.State{Factions: []game.FactionState{{FactionID: "own", Gold: 12}}}
	for i := 0; i < 20; i++ {
		owner := "own"
		if i%2 == 1 {
			owner = "visible-other"
		}
		state.Units = append(state.Units, game.Unit{ID: fmt.Sprintf("%s:u%d", owner, i), FactionID: owner, Type: "ranger", Health: int64(i), ReadyAtTick: 51, AttackTargetID: "own:u0", Position: game.Position{X: i, Y: 2}})
		state.Buildings = append(state.Buildings, game.Building{ID: fmt.Sprintf("%s:b%d", owner, i), FactionID: owner, Type: "tower", MaxHealth: 100, Health: int64(80 + i), Defense: int64(i % 2), Income: 12, Attack: 9, Range: 3, ReadyAtTick: 50, AttackOutcome: "hit", AttackTargetID: "own:u0"})
	}
	original := contextObject(map[string]any{"view": game.View{State: state}})
	before := string(contextJSON(original))
	got := groupContextFactions(original)
	view := contextObject(got["view"])
	groupedState := contextObject(view["state"])
	if groupedState["units"] != nil || groupedState["buildings"] != nil {
		t.Fatal("ungrouped duplicates")
	}
	var groups []factionObservationGroup
	_ = json.Unmarshal(groupedState["factions"], &groups)
	if len(groups) != 2 || groups[0].FactionID != "own" || groups[1].FactionID != "visible-other" || groups[1].Stats != nil {
		t.Fatal("ownership or missing statistics changed")
	}
	var profiles map[string]map[string]json.RawMessage
	_ = json.Unmarshal(view["buildingProfiles"], &profiles)
	if len(profiles) != 2 {
		t.Fatal("different effective building defenses merged")
	}
	expected := map[string]json.RawMessage{}
	for _, unit := range state.Units {
		expected[unit.ID] = contextJSON(contextObject(unit))
	}
	for _, building := range state.Buildings {
		expected[building.ID] = contextJSON(contextObject(building))
	}
	seen := assertGroupedEntities(t, groups, profiles, expected)
	if seen != len(expected) {
		t.Fatal("entity missing")
	}
	if len(contextJSON(got)) >= len(before) {
		t.Fatal("grouped fixture grew")
	}
	if string(contextJSON(map[string]any{"view": game.View{State: state}})) != before {
		t.Fatal("persisted state changed")
	}
}

func TestBuildingProfilesKeepSmallSetsInline(t *testing.T) {
	for _, buildings := range [][]map[string]json.RawMessage{nil, {contextObject(game.Building{ID: "castle", Type: "castle"})}} {
		items, profiles := buildingProfiles(buildings)
		if profiles != nil || !sameContextJSON(contextJSON(items), contextJSON(buildings)) {
			t.Fatal("small set changed")
		}
	}
}

func assertGroupedEntity(t *testing.T, item map[string]json.RawMessage, factionID string, profiles map[string]map[string]json.RawMessage, expected map[string]json.RawMessage) {
	t.Helper()
	if item["factionId"] != nil {
		t.Fatal("ownership repeated")
	}
	item["factionId"] = contextJSON(factionID)
	if ref := item["profile"]; ref != nil {
		var id string
		_ = json.Unmarshal(ref, &id)
		delete(item, "profile")
		for k, v := range profiles[id] {
			item[k] = v
		}
	}
	var id string
	_ = json.Unmarshal(item["id"], &id)
	if !sameContextJSON(contextJSON(item), expected[id]) {
		t.Fatalf("entity changed: %s", id)
	}
}

func assertGroupedEntities(t *testing.T, groups []factionObservationGroup, profiles map[string]map[string]json.RawMessage, expected map[string]json.RawMessage) int {
	t.Helper()
	seen := 0
	for _, group := range groups {
		for _, kind := range [][]map[string]json.RawMessage{group.Units, group.Buildings} {
			for _, item := range kind {
				assertGroupedEntity(t, item, group.FactionID, profiles, expected)
				seen++
			}
		}
	}
	return seen
}
