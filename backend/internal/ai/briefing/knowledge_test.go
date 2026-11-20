// Checks knowledge selection stays within visibility, frozen-rule, card-count, and byte-
// budget boundaries.

package briefing

import (
	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai/decision"

	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/game"
)

func knowledgeFixture() (game.Ruleset, ai.DecisionContext) {
	r := game.Ruleset{ID: "test", Version: "1", Knowledge: &game.RulesKnowledge{Mechanics: "Base rules"}}
	for i := 0; i < 24; i++ {
		id := fmt.Sprintf("unit-%02d", i)
		r.Units = append(r.Units, game.UnitType{ID: id, Cost: int64(i + 1)})
		r.Knowledge.Cards = append(r.Knowledge.Cards, game.UnitCard{UnitID: id, Role: "role", Strengths: "strength", Weaknesses: "weakness", Guidance: "guide"})
	}
	c := ai.DecisionContext{FactionID: "blue", Legal: ai.LegalChoices{Units: r.Units}}
	c.View.State.Units = []game.Unit{{Type: "unit-20", FactionID: "red"}, {Type: "unit-19", FactionID: "blue"}}
	c.ActivePlan.RecruitmentPriorities = []string{"unit-18", "unit-20"}
	return r, c
}
func TestKnowledgeSelectionAndBudgets(t *testing.T) {
	r, c := knowledgeFixture()
	for _, count := range []int{4, 16, 24} {
		checkKnowledgeCardBudget(t, r, c, count)
	}
	k, _ := RetrieveKnowledge(r, c, ai.KnowledgeLimits{MaxUnitCards: 24, MaxBytes: 1000})
	if k == nil || !k.Truncated || k.SerializedSize > 1000 {
		t.Fatalf("byte budget: %+v", k)
	}
	if tiny, _ := RetrieveKnowledge(r, c, ai.KnowledgeLimits{MaxUnitCards: 1, MaxBytes: 1}); tiny != nil {
		t.Fatal("unrepresentable envelope")
	}
	if _, err := RetrieveKnowledge(r, c, ai.KnowledgeLimits{}); err == nil {
		t.Fatal("invalid limits")
	}
	r.Knowledge = nil
	if k, err := RetrieveKnowledge(r, c, ai.DefaultKnowledgeLimits()); err != nil || k != nil {
		t.Fatal("legacy rules")
	}
}

// checkKnowledgeCardBudget requires exactly count cards, most relevant first, an
// accurate serialized size, and truncation reported below the full 24 cards.
func checkKnowledgeCardBudget(t *testing.T, r game.Ruleset, c ai.DecisionContext, count int) {
	t.Helper()
	k, err := RetrieveKnowledge(r, c, ai.KnowledgeLimits{MaxUnitCards: count, MaxBytes: 65536})
	if err != nil {
		t.Fatal(err)
	}
	if len(k.Cards) != count || !reflect.DeepEqual(k.SelectedIDs[:3], []string{"unit-20", "unit-19", "unit-18"}) {
		t.Fatalf("selection: %+v", k)
	}
	payload, _ := json.Marshal(k)
	if len(payload) != k.SerializedSize {
		t.Fatal("size mismatch")
	}
	if k.Truncated != (count < 24) {
		t.Fatal("truncation mismatch")
	}
}
func TestKnowledgeUsesVisibleContextAndFrozenRules(t *testing.T) {
	r, c := knowledgeFixture()
	first, _ := RetrieveKnowledge(r, c, ai.DefaultKnowledgeLimits())
	again, _ := RetrieveKnowledge(r, c, ai.DefaultKnowledgeLimits())
	if !reflect.DeepEqual(first, again) {
		t.Fatal("nondeterministic")
	}
	clone := game.CloneScenario(game.Scenario{Ruleset: r})
	r.Knowledge.Cards[0].Role = "changed"
	r.Knowledge.Mechanics = "changed"
	frozen, _ := RetrieveKnowledge(clone.Ruleset, c, ai.DefaultKnowledgeLimits())
	if !reflect.DeepEqual(first, frozen) {
		t.Fatal("snapshot changed")
	}
	c.View.State.Units = nil
	k, _ := RetrieveKnowledge(clone.Ruleset, c, ai.KnowledgeLimits{MaxUnitCards: 1, MaxBytes: 65536})
	if k.SelectedIDs[0] != "unit-18" {
		t.Fatal("selection inferred hidden enemy")
	}
	c.Knowledge = first
	h1, _ := decision.ObservationHash(c)
	c.Knowledge = k
	h2, _ := decision.ObservationHash(c)
	if h1 == h2 {
		t.Fatal("knowledge excluded from hash")
	}
}
