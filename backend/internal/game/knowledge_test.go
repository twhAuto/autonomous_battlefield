// Checks rules knowledge references and validation against available unit definitions.

package game

import (
	"strings"
	"testing"
)

func TestKnowledgeValidation(t *testing.T) {
	valid := Ruleset{Units: []UnitType{{ID: "new"}}, Knowledge: &RulesKnowledge{Mechanics: "mechanics", Cards: []UnitCard{{UnitID: "new", Role: "role", Strengths: "strengths", Weaknesses: "weaknesses", Guidance: "guidance"}}}}
	if err := ValidateKnowledge(valid); err != nil {
		t.Fatal(err)
	}
	cases := []func(*RulesKnowledge){func(k *RulesKnowledge) { k.Mechanics = "" }, func(k *RulesKnowledge) { k.Mechanics = strings.Repeat("x", 4097) }, func(k *RulesKnowledge) { k.Cards = nil }, func(k *RulesKnowledge) { k.Cards[0].UnitID = "unknown" }, func(k *RulesKnowledge) { k.Cards = append(k.Cards, k.Cards[0]) }, func(k *RulesKnowledge) { k.Cards[0].Role = "" }, func(k *RulesKnowledge) { k.Cards[0].Guidance = strings.Repeat("x", 1025) }}
	for i, change := range cases {
		r := valid
		r.Knowledge = CloneKnowledge(valid.Knowledge)
		change(r.Knowledge)
		if ValidateKnowledge(r) == nil {
			t.Fatalf("accepted invalid case %d", i)
		}
	}
	if CloneKnowledge(nil) != nil {
		t.Fatal("nil clone")
	}
}
