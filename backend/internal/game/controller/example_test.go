// Provides an executable example of configuring a utility controller and applying a
// scheduled plan.

package controller_test

import (
	"fmt"

	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
)

func ExampleUtilityController() {
	utilityController, _ := controller.NewUtilityController(controller.Config{
		DecisionIntervalTicks: 10,
		DecisionLeadTicks:     2,
		DefaultActionWeight:   1000,
		ActionKinds:           []string{"attack", "defend"},
		FallbackPlan: controller.PlanProposal{
			Name:          "safe fallback",
			ActionWeights: map[string]int64{"attack": 500, "defend": 1500},
		},
		Traits: []controller.Trait{{
			ID:                    "aggressive",
			ActionWeightModifiers: map[string]int64{"attack": 250},
		}},
	})

	observation := controller.Observation{
		BattlefieldID: "battlefield-1",
		FactionID:     "faction-1",
		Tick:          0,
		Version:       7,
		Candidates: []controller.Candidate{
			{ID: "attack-north", Kind: "attack", TargetID: "north", BaseUtility: 100},
			{ID: "defend-home", Kind: "defend", TargetID: "home", BaseUtility: 80},
		},
	}

	result, _ := utilityController.Tick(observation)
	fmt.Println(result.Selection.CandidateID, result.UsedFallback, result.DecisionRequest != nil)

	_, _ = utilityController.SubmitPlan(result.DecisionRequest.ID, controller.PlanProposal{
		Name:          "push north",
		ActionWeights: map[string]int64{"attack": 2000, "defend": 500},
		TargetBonuses: map[string]int64{"north": 10_000},
	})
	observation.Tick = 2
	observation.Version++
	result, _ = utilityController.Tick(observation)
	fmt.Println(result.Selection.CandidateID, result.UsedFallback)

	// Output:
	// defend-home true true
	// attack-north false
}
