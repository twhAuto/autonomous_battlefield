// Checks opening-decision submissions reject invalid upgrade choices.

package prompts

import (
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/decision"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
)

func TestOpeningSubmissionValidatesUpgrades(t *testing.T) {
	record := ai.DecisionRecord{ID: "opening", Version: 1, Observation: ai.DecisionContext{Opening: true, Legal: ai.LegalChoices{ActionKinds: []string{"attack"}, Upgrades: []game.Upgrade{{ID: "plating", Cost: 20}}}}}
	empty := []controller.RecruitmentOrder{}
	plan := controller.PlanProposal{Name: "save", RecruitmentOrders: &empty, UpgradeOrders: []string{"plating"}, SpendOrder: []string{"upgrades"}}
	submission := ai.PlanSubmission{RequestID: "opening", IdempotencyKey: "once", ExpectedVersion: 1, TargetTick: 0, Plan: plan}
	if issues := decision.ValidateSubmission(record, submission); len(issues) != 0 {
		t.Fatal(issues)
	}
	for _, mutate := range []func(*controller.PlanProposal){
		func(p *controller.PlanProposal) { p.UpgradeOrders = []string{"unknown"} },
		func(p *controller.PlanProposal) { p.UpgradeOrders = []string{"plating", "plating"} },
		func(p *controller.PlanProposal) { p.RecruitmentOrders = nil },
		func(p *controller.PlanProposal) { p.SpendOrder = []string{"construction"} },
	} {
		submission.Plan = controller.ClonePlanProposal(plan)
		mutate(&submission.Plan)
		if len(decision.ValidateSubmission(record, submission)) == 0 {
			t.Fatal("invalid opening accepted")
		}
	}
}
