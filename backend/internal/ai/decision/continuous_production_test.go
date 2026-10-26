package decision

import (
	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
	"testing"
)

func TestContinuousFrozenValidation(t *testing.T) {
	record := ai.DecisionRecord{FactionID: "a", Runtime: "cli", Observation: ai.DecisionContext{ContinuousProduction: true, Opening: true, Legal: ai.LegalChoices{ActionKinds: []string{"attack"}, RecruitmentSources: []string{"a:castle"}}, View: game.View{State: game.State{Buildings: []game.Building{{ID: "a:castle", FactionID: "a", Health: 100, Trains: []string{"vanguard"}}}}}}}
	for _, tc := range []struct {
		name, building, unit string
		enabled, valid       bool
	}{
		{"enable", "a:castle", "vanguard", true, true}, {"disable", "a:castle", "", false, true},
		{"enemy", "b:castle", "vanguard", true, false}, {"wrong roster", "a:castle", "ranger", true, false}, {"missing selection", "a:castle", "", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			orders := []controller.ProductionOrder{{BuildingID: tc.building, UnitType: tc.unit, Enabled: tc.enabled}}
			submission := ai.PlanSubmission{IdempotencyKey: "once", Plan: controller.PlanProposal{Name: "choose", ProductionOrders: &orders}}
			if issues := ValidateSubmission(record, submission); (len(issues) == 0) != tc.valid {
				t.Fatal(issues)
			}
		})
	}
}

func TestContinuousRejectsLegacyPurchasesAndRequiresOpeningChoice(t *testing.T) {
	record := ai.DecisionRecord{Observation: ai.DecisionContext{ContinuousProduction: true, Opening: true, Legal: ai.LegalChoices{ActionKinds: []string{"attack"}, Units: []game.UnitType{{ID: "vanguard"}}}}}
	orders := []controller.RecruitmentOrder{{UnitType: "vanguard", Count: 1}}
	submission := ai.PlanSubmission{IdempotencyKey: "once", Plan: controller.PlanProposal{Name: "old", RecruitmentOrders: &orders}}
	if len(ValidateSubmission(record, submission)) < 2 {
		t.Fatal("legacy batch accepted in new mode")
	}
	empty := []controller.ProductionOrder{}
	submission.Plan = controller.PlanProposal{Name: "off", ProductionOrders: &empty}
	if issues := ValidateSubmission(record, submission); len(issues) != 0 {
		t.Fatal(issues)
	}
	record.Observation.ContinuousProduction = false
	if len(ValidateSubmission(record, submission)) == 0 {
		t.Fatal("new orders accepted in legacy mode")
	}
}
