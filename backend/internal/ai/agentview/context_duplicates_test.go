package agentview

import (
	"encoding/json"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
)

func TestContextDuplicatesPreserveDifferentFacts(t *testing.T) {
	source := json.RawMessage(`{"battleId":"b","observationVersion":12,"factionId":"own","ownedUpgradeIds":["armor"],"economy":{"gold":25},"activePlan":{"name":"defend","rationale":"Keep the castle safe"},"memory":[{"plan":{"rationale":"Keep the castle safe","name":"defend"}},{"plan":{"name":"attack"}}],"view":{"state":{"battleId":"b","tick":12,"factions":[{"factionId":"own","gold":25,"ownedUpgradeIds":["armor"]},{"factionId":"enemy","gold":25,"ownedUpgradeIds":["armor"]}]}}}`)
	before := string(source)
	result := deduplicateContext(source)
	if len(contextJSON(result)) >= len(source) {
		t.Fatal("did not shrink")
	}
	var memory []map[string]json.RawMessage
	_ = json.Unmarshal(result["memory"], &memory)
	if string(memory[0]["planRef"]) != `"#/activePlan"` || memory[0]["plan"] != nil || memory[1]["plan"] == nil {
		t.Fatal("plan reference lost historical difference")
	}
	view := contextObject(result["view"])
	state := contextObject(view["state"])
	if state["tick"] != nil || state["battleId"] != nil {
		t.Fatal("redundant identity retained")
	}
	var factions []map[string]json.RawMessage
	_ = json.Unmarshal(state["factions"], &factions)
	if factions[0]["gold"] != nil || factions[0]["ownedUpgradeIds"] != nil || factions[1]["gold"] == nil || factions[1]["ownedUpgradeIds"] == nil {
		t.Fatal("faction scoping lost")
	}
	state["tick"] = contextJSON(13)
	state["battleId"] = contextJSON("different")
	view["state"] = contextJSON(state)
	result["view"] = contextJSON(view)
	second := deduplicateContext(result)
	if !sameContextJSON(contextObject(second["view"])["state"], view["state"]) {
		t.Fatal("different metadata removed")
	}
	if string(source) != before {
		t.Fatal("stored input changed")
	}
}

func TestSessionContextIdentityIsSuppliedExactlyOnce(t *testing.T) {
	payload := json.RawMessage(`{"context":{"factionName":"WH","background":"Full story","doctrine":"Defend","requestId":"r"}}`)
	s := ai.DecisionSession{FactionName: "WH", Background: "Full story", Doctrine: "Defend"}
	got := SessionContextPayload(s, payload)
	if string(got) != `{"context":{"requestId":"r"}}` {
		t.Fatal(string(got))
	}
	for _, session := range []ai.DecisionSession{{}, {FactionName: "Other", Background: "Different", Doctrine: "Attack"}, {JobType: "faction_background", Background: "Full story"}} {
		if string(SessionContextPayload(session, payload)) != string(payload) {
			t.Fatal("unsupplied identity removed")
		}
	}
	for _, input := range []string{"invalid", `{}`, `{"context":null}`} {
		if string(SessionContextPayload(s, []byte(input))) != input {
			t.Fatal("invalid envelope changed")
		}
	}
}
