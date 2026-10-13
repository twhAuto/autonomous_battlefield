// Checks knowledge selection stays within visibility, frozen-rule, card-count, and byte-
// budget boundaries.

package briefing

import (
	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/decision"
	"github.com/twh09/autonomous_battlefield/backend/internal/testutil"

	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/game"
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
		k, err := RetrieveKnowledge(r, c, ai.KnowledgeLimits{MaxUnitCards: count, MaxBytes: 65536})
		testutil.Require(t, err == nil, func() { t.Fatal(err) })
		testutil.Require(t, !(len(k.Cards) != count || !reflect.DeepEqual(k.SelectedIDs[:3], []string{"unit-20", "unit-19", "unit-18"})), func() { t.Fatalf("selection: %+v", k) })
		payload, _ := json.Marshal(k)
		testutil.Require(t, len(payload) == k.SerializedSize, func() { t.Fatal("size mismatch") })
		testutil.Require(t, k.Truncated == (count < 24), func() { t.Fatal("truncation mismatch") })
	}
	k, _ := RetrieveKnowledge(r, c, ai.KnowledgeLimits{MaxUnitCards: 24, MaxBytes: 1000})
	testutil.Require(t, !(k == nil || !k.Truncated || k.SerializedSize > 1000), func() { t.Fatalf("byte budget: %+v", k) })
	{
		tiny, _ := RetrieveKnowledge(r, c, ai.KnowledgeLimits{MaxUnitCards: 1, MaxBytes: 1})
		testutil.Require(t, tiny == nil, func() { t.Fatal("unrepresentable envelope") })
	}
	{
		_, err := RetrieveKnowledge(r, c, ai.KnowledgeLimits{})
		testutil.Require(t, err != nil, func() { t.Fatal("invalid limits") })
	}
	r.Knowledge = nil
	{
		k, err := RetrieveKnowledge(r, c, ai.DefaultKnowledgeLimits())
		testutil.Require(t, !(err != nil || k != nil), func() { t.Fatal("legacy rules") })
	}
}
func TestKnowledgeUsesVisibleContextAndFrozenRules(t *testing.T) {
	r, c := knowledgeFixture()
	first, _ := RetrieveKnowledge(r, c, ai.DefaultKnowledgeLimits())
	again, _ := RetrieveKnowledge(r, c, ai.DefaultKnowledgeLimits())
	testutil.Require(t, reflect.DeepEqual(first, again), func() { t.Fatal("nondeterministic") })
	clone := game.CloneScenario(game.Scenario{Ruleset: r})
	r.Knowledge.Cards[0].Role = "changed"
	r.Knowledge.Mechanics = "changed"
	frozen, _ := RetrieveKnowledge(clone.Ruleset, c, ai.DefaultKnowledgeLimits())
	testutil.Require(t, reflect.DeepEqual(first, frozen), func() { t.Fatal("snapshot changed") })
	c.View.State.Units = nil
	k, _ := RetrieveKnowledge(clone.Ruleset, c, ai.KnowledgeLimits{MaxUnitCards: 1, MaxBytes: 65536})
	testutil.Require(t, k.SelectedIDs[0] == "unit-18", func() { t.Fatal("selection inferred hidden enemy") })
	c.Knowledge = first
	h1, _ := decision.ObservationHash(c)
	c.Knowledge = k
	h2, _ := decision.ObservationHash(c)
	testutil.Require(t, h1 != h2, func() { t.Fatal("knowledge excluded from hash") })
}
