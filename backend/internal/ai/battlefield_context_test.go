package ai

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/game"
)

// Optional local measurement; never commit recorded battle context as a fixture.
func TestBattlefieldContextExternalFixture(t *testing.T) {
	path := os.Getenv("BATTLEFIELD_CONTEXT_FIXTURE")
	if path == "" {
		t.Skip("set BATTLEFIELD_CONTEXT_FIXTURE for a local JSON context")
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var source map[string]json.RawMessage
	if err := json.Unmarshal(payload, &source); err != nil {
		payload = append([]byte("{"), payload...)
		if err := json.Unmarshal(payload, &source); err != nil {
			t.Fatal(err)
		}
	}
	var view game.View
	if err := json.Unmarshal(source["view"], &view); err != nil {
		t.Fatal(err)
	}
	before := len(contextJSON(source))
	if contextObject(source["view"])["unitProfiles"] == nil && contextObject(source["view"])["visibleRowRanges"] == nil {
		source["view"] = contextJSON(compactBattlefieldView(view))
	}
	source = deduplicateContext(source)
	source = groupContextFactions(source)
	after := len(contextJSON(source))
	if after > before {
		t.Fatal("context grew")
	}
	t.Logf("initial context bytes before=%d after=%d saved=%d (%.1f%%)", before, after, before-after, 100*float64(before-after)/float64(before))
	var session DecisionSession
	_ = json.Unmarshal(source["factionName"], &session.FactionName)
	_ = json.Unmarshal(source["background"], &session.Background)
	_ = json.Unmarshal(source["doctrine"], &session.Doctrine)
	envelope := contextJSON(map[string]any{"context": source})
	t.Logf("API/Gemini identity dedup saves an additional %d bytes from the delivered context", len(envelope)-len(SessionContextPayload(session, envelope)))
}

func TestBattlefieldCompressionPreservesObservations(t *testing.T) {
	for _, count := range []int{0, 1, 46, 200} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			view := compressionView(count)
			before := contextJSON(view)
			projected := compactBattlefieldView(view)
			if len(contextJSON(projected)) > len(before) {
				t.Fatal("view grew")
			}
			if string(contextJSON(view)) != string(before) {
				t.Fatal("source mutated")
			}
			state := restoreProfiledUnits(t, projected)
			var restored game.State
			_ = json.Unmarshal(contextJSON(state), &restored)
			if !reflect.DeepEqual(restored, view.State) {
				t.Fatal("unit observations changed")
			}
			positions := restoreVisibleRows(projected)
			if !reflect.DeepEqual(positions, view.VisiblePositions) {
				t.Fatal("visibility changed, including empty cells or gaps")
			}
			t.Logf("view bytes before=%d after=%d", len(before), len(contextJSON(projected)))
		})
	}
}

func TestBattlefieldContextVersionCompatibility(t *testing.T) {
	_, c := compactFixture()
	c.SchemaVersion = "battlefield.decision-context.v3"
	before := string(contextJSON(c))
	var result map[string]json.RawMessage
	_ = json.Unmarshal(contextJSON(AgentObservation(c)), &result)
	if string(contextJSON(c)) != before {
		t.Fatal("stored observation mutated")
	}
	if string(result["schemaVersion"]) != `"battlefield.decision-context.v4"` {
		t.Fatal("missing version")
	}
	for _, k := range []string{"requestId", "expectedVersion", "targetTick"} {
		if string(result[k]) != string(contextObject(c)[k]) {
			t.Fatal("submission metadata changed")
		}
	}
	// Tiny/empty views retain their original representation if encoding expands it.
	v := game.View{VisiblePositions: []game.Position{{X: 2, Y: 3}}}
	if string(contextJSON(compactBattlefieldView(v))) != string(contextJSON(contextObject(v))) {
		t.Fatal("tiny view should stay inline")
	}
}

func compressionView(count int) game.View {
	view := game.View{}
	for i := 0; i < count; i++ {
		view.State.Units = append(view.State.Units, game.Unit{ID: fmt.Sprint(i), FactionID: "own", Type: "ranger", Attack: int64(10 + i%3), Health: int64(i), MaxHealth: 50, ReadyAtTick: uint64(i), Position: game.Position{X: i % 25, Y: i / 25}, MovementPath: []game.Position{{X: 1, Y: 2}}, HealTargetID: "ally", AttackOutcome: "hit"})
	}
	for y := 0; y < 17; y++ {
		for x := 0; x < 25; x++ {
			if x != 12 {
				view.VisiblePositions = append(view.VisiblePositions, game.Position{X: x, Y: y})
			}
		}
	}
	return view
}

func restoreProfiledUnits(t *testing.T, projected map[string]json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	state := contextObject(json.RawMessage(projected["state"]))
	if raw, ok := projected["unitProfiles"]; ok {
		var profiles map[string]map[string]json.RawMessage
		var units []map[string]json.RawMessage
		_ = json.Unmarshal(raw, &profiles)
		_ = json.Unmarshal(state["units"], &units)
		if len(profiles) != 3 {
			t.Fatalf("distinct effective stats merged: %d", len(profiles))
		}
		for _, u := range units {
			var id string
			_ = json.Unmarshal(u["profile"], &id)
			delete(u, "profile")
			for k, v := range profiles[id] {
				u[k] = v
			}
		}
		state["units"] = contextJSON(units)
	}
	return state
}

func restoreVisibleRows(projected map[string]json.RawMessage) []game.Position {
	var rows []visibleRow
	_ = json.Unmarshal(projected["visibleRowRanges"], &rows)
	positions := []game.Position{}
	for _, row := range rows {
		for _, span := range row.XRanges {
			for x := span[0]; x <= span[1]; x++ {
				positions = append(positions, game.Position{X: x, Y: row.Y})
			}
		}
	}
	return positions
}
