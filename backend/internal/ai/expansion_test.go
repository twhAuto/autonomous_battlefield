package ai

import (
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
	"testing"
)

func TestExpansionOrdersValidateAgainstFrozenChoices(t *testing.T) {
	r := DecisionRecord{Version: 1, TargetTick: 10, Observation: DecisionContext{Legal: LegalChoices{ActionKinds: []string{"attack"}, Units: []game.UnitType{{ID: "catapult"}}, Buildings: []game.BuildingType{{ID: "defense_tower"}}, EconomySites: []controller.Coordinate{{X: 9, Y: 7}}, RecruitmentSources: []string{"a:barracks:1"}}}}
	construction := []controller.ConstructionOrder{{BuildingType: "defense_tower", Position: controller.Coordinate{X: 9, Y: 7}}}
	recruits := []controller.RecruitmentOrder{{UnitType: "catapult", Count: 1, SourceBuildingID: "a:barracks:1"}}
	s := PlanSubmission{ExpectedVersion: 1, TargetTick: 10, IdempotencyKey: "once", Plan: controller.PlanProposal{Name: "siege", ConstructionOrders: &construction, RecruitmentOrders: &recruits}}
	if issues := ValidateSubmission(r, s); len(issues) != 0 {
		t.Fatal(issues)
	}
	for _, change := range []func(*DecisionRecord, *PlanSubmission){
		func(r *DecisionRecord, _ *PlanSubmission) { r.Observation.Opening = true },
		func(_ *DecisionRecord, s *PlanSubmission) { (*s.Plan.ConstructionOrders)[0].BuildingType = "barracks" },
		func(_ *DecisionRecord, s *PlanSubmission) { (*s.Plan.ConstructionOrders)[0].Position.X = 15 },
		func(_ *DecisionRecord, s *PlanSubmission) {
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
