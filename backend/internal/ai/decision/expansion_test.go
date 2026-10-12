package decision

import (
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
)

func TestExpansionOrdersValidateAgainstFrozenChoices(t *testing.T) {
	r := ai.DecisionRecord{Version: 1, TargetTick: 10, Observation: ai.DecisionContext{Legal: ai.LegalChoices{ActionKinds: []string{"attack"}, Units: []game.UnitType{{ID: "catapult"}}, Buildings: []game.BuildingType{{ID: "defense_tower"}}, EconomySites: []controller.Coordinate{{X: 9, Y: 7}}, RecruitmentSources: []string{"a:barracks:1"}}}}
	construction := []controller.ConstructionOrder{{BuildingType: "defense_tower", Position: controller.Coordinate{X: 9, Y: 7}}}
	recruits := []controller.RecruitmentOrder{{UnitType: "catapult", Count: 1, SourceBuildingID: "a:barracks:1"}}
	s := ai.PlanSubmission{ExpectedVersion: 1, TargetTick: 10, IdempotencyKey: "once", Plan: controller.PlanProposal{Name: "siege", ConstructionOrders: &construction, RecruitmentOrders: &recruits}}
	if issues := ValidateSubmission(r, s); len(issues) != 0 {
		t.Fatal(issues)
	}
	for _, change := range []func(*ai.DecisionRecord, *ai.PlanSubmission){
		func(r *ai.DecisionRecord, _ *ai.PlanSubmission) { r.Observation.Opening = true },
		func(_ *ai.DecisionRecord, s *ai.PlanSubmission) {
			(*s.Plan.ConstructionOrders)[0].BuildingType = "barracks"
		},
		func(_ *ai.DecisionRecord, s *ai.PlanSubmission) { (*s.Plan.ConstructionOrders)[0].Position.X = 15 },
		func(_ *ai.DecisionRecord, s *ai.PlanSubmission) {
			(*s.Plan.RecruitmentOrders)[0].SourceBuildingID = "b:barracks:1"
		},
	} {
		copyR, copyS := r, s
		copyS.Plan = controller.ClonePlanProposal(s.Plan)
		change(&copyR, &copyS)
		if issues := ValidateSubmission(copyR, copyS); len(issues) == 0 {
			t.Fatal("invalid expansion choice accepted")
		}
	}
}
