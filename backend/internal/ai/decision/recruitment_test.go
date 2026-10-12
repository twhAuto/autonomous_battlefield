// Checks explicit unit purchases are validated against the decision context before
// submission.

package decision

import (
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
)

func TestDirectPurchaseSubmission(t *testing.T) {
	record := ai.DecisionRecord{Version: 1, TargetTick: 10, Runtime: "api", Observation: ai.DecisionContext{Legal: ai.LegalChoices{ActionKinds: []string{"attack"}, Units: []game.UnitType{{ID: "ranger"}}}}}
	orders := []controller.RecruitmentOrder{{UnitType: "ranger", Count: 2}}
	submission := ai.PlanSubmission{ExpectedVersion: 1, TargetTick: 10, IdempotencyKey: "once", Plan: controller.PlanProposal{Name: "buy", RecruitmentOrders: &orders}}
	if issues := ValidateSubmission(record, submission); len(issues) != 0 {
		t.Fatal(issues)
	}
	orders[0].UnitType = "unknown"
	if issues := ValidateSubmission(record, submission); len(issues) == 0 {
		t.Fatal("unknown purchase accepted")
	}
	orders = []controller.RecruitmentOrder{}
	submission.Plan.RecruitmentOrders = &orders
	if issues := ValidateSubmission(record, submission); len(issues) != 0 {
		t.Fatal("explicit no-purchase decision rejected", issues)
	}
}
