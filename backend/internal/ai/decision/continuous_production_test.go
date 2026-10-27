package decision

import (
	"strings"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
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

func TestLockedUnitProductionRequiresUnlock(t *testing.T) {
	legal := ai.LegalChoices{ActionKinds: []string{"attack"}, RecruitmentSources: []string{"a:workshop"}, Units: []game.UnitType{{ID: "cannon", RequiresUpgradeID: "gunpowder"}}, Upgrades: []game.Upgrade{{ID: "gunpowder", Cost: 1000}}}
	view := game.View{State: game.State{Buildings: []game.Building{{ID: "a:workshop", FactionID: "a", Health: 100, Trains: []string{"cannon"}}}}}
	record := ai.DecisionRecord{FactionID: "a", Runtime: "cli", Observation: ai.DecisionContext{ContinuousProduction: true, Legal: legal, View: view}}
	orders := []controller.ProductionOrder{{BuildingID: "a:workshop", UnitType: "cannon", Enabled: true}}
	submission := ai.PlanSubmission{IdempotencyKey: "once", Plan: controller.PlanProposal{Name: "guns", ProductionOrders: &orders}}
	issues := ValidateSubmission(record, submission)
	if len(issues) != 1 || issues[0].Code != "locked" || !strings.Contains(issues[0].Message, `"gunpowder"`) {
		t.Fatalf("locked production accepted: %+v", issues)
	}
	submission.Plan.UpgradeOrders, submission.Plan.SpendOrder = []string{"gunpowder"}, []string{"upgrades"}
	if issues := ValidateSubmission(record, submission); len(issues) != 0 {
		t.Fatalf("ordered unlock rejected: %+v", issues)
	}
	submission.Plan.UpgradeOrders, record.Observation.OwnedUpgradeIDs = nil, []string{"gunpowder"}
	if issues := ValidateSubmission(record, submission); len(issues) != 0 {
		t.Fatalf("owned unlock rejected: %+v", issues)
	}
}
