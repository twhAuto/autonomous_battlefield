package agentview

import (
	"encoding/json"
	"testing"
)

const idFixture = `{"view":{"state":{"factions":[
{"factionId":"blue","units":[{"id":"blue:vanguard:001","attackTargetId":"red:ranger:004"},{"id":"blue:herbalist:002","healTargetId":"blue:vanguard:001"}],
 "buildings":[{"id":"blue:castle","health":300,"attackTargetId":"red:ranger:004"},{"id":"blue:outpost:003","health":0}]},
{"factionId":"red","units":[{"id":"red:ranger:004","attackTargetId":"blue:castle"},{"id":"red:ranger:005","attackTargetId":"blue:scout:009"}],
 "buildings":[{"id":"red:stables:002","health":0}]}],
"unitDeaths":[{"id":"blue:scout:009","factionId":"blue","type":"scout"}]}}}`

func TestCompactUnitIDsRewritesUnitReferences(t *testing.T) {
	got := contextJSON(compactUnitIDs(contextObject(json.RawMessage(idFixture))))
	want := `{"view":{"state":{"factions":[
{"factionId":"blue","units":[{"id":1,"attackTargetId":3},{"id":2,"healTargetId":1}],"buildings":[{"id":"blue:castle","health":300,"attackTargetId":3}]},
{"factionId":"red","units":[{"id":3,"attackTargetId":"blue:castle"},{"id":4,"attackTargetId":5}]}],
"unitDeaths":[{"id":5,"faction":0,"type":"scout"}]}}}`
	if !sameContextJSON(got, json.RawMessage(want)) {
		t.Fatalf("short IDs:\n%s", got)
	}
}

func TestCompactUnitIDsLeavesUnexpectedShapesUnchanged(t *testing.T) {
	for _, fixture := range []string{
		`{}`, `{"view":[]}`, `{"view":{"state":[]}}`, `{"view":{"state":{"factions":{}}}}`,
		`{"view":{"state":{"factions":[null]}}}`, `{"view":{"state":{"factions":[],"unitDeaths":[null]}}}`,
		`{"view":{"state":{"factions":[{"units":[{"id":1}]}]}}}`,
		`{"view":{"state":{"factions":[{"units":[{"id":"a"},{"id":"a"}]}]}}}`,
		`{"view":{"state":{"factions":[{"units":[{"id":"a"}],"buildings":{}}]}}}`,
	} {
		if got := contextJSON(compactUnitIDs(contextObject(json.RawMessage(fixture)))); !sameContextJSON(got, json.RawMessage(fixture)) {
			t.Fatalf("%s changed to %s", fixture, got)
		}
	}
}
