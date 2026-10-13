// Builds the shared ruleset and decision-context fixture used by agent projection tests.

package agentview

import (
	"fmt"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
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
