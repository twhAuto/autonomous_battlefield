// Checks plan submissions against frozen legal choices: direct purchases, construction and
// recruitment sources, and opening-decision rules.

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

func TestOpeningSubmissionValidatesUpgrades(t *testing.T) {
	record := ai.DecisionRecord{ID: "opening", Version: 1, Observation: ai.DecisionContext{Opening: true, Legal: ai.LegalChoices{ActionKinds: []string{"attack"}, Upgrades: []game.Upgrade{{ID: "plating", Cost: 20}}}}}
	empty := []controller.RecruitmentOrder{}
	plan := controller.PlanProposal{Name: "save", RecruitmentOrders: &empty, UpgradeOrders: []string{"plating"}, SpendOrder: []string{"upgrades"}}
	submission := ai.PlanSubmission{RequestID: "opening", IdempotencyKey: "once", ExpectedVersion: 1, TargetTick: 0, Plan: plan}
	if issues := ValidateSubmission(record, submission); len(issues) != 0 {
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
		if len(ValidateSubmission(record, submission)) == 0 {
			t.Fatal("invalid opening accepted")
		}
	}
}

func TestUpgradePrerequisitesUseFrozenOwnership(t *testing.T) {
	r := ai.DecisionRecord{Runtime: "api", Observation: ai.DecisionContext{Legal: ai.LegalChoices{ActionKinds: []string{"attack"}, Upgrades: []game.Upgrade{
		{ID: "arsenal", Track: "arsenal", Level: 1, Cost: 20},
		{ID: "arsenal_2", Track: "arsenal", Level: 2, RequiresUpgradeID: "arsenal", Cost: 50},
	}}}}
	s := ai.PlanSubmission{IdempotencyKey: "levels", Plan: controller.PlanProposal{Name: "upgrade", SpendOrder: []string{"upgrades"}, UpgradeOrders: []string{"arsenal_2"}}}
	if issues := ValidateSubmission(r, s); len(issues) == 0 {
		t.Fatal("missing prerequisite accepted")
	}
	r.Observation.OwnedUpgradeIDs = []string{"arsenal"}
	if issues := ValidateSubmission(r, s); len(issues) != 0 {
		t.Fatal(issues)
	}
	r.Observation.OwnedUpgradeIDs = nil
	s.Plan.UpgradeOrders = []string{"arsenal", "arsenal_2"}
	if issues := ValidateSubmission(r, s); len(issues) != 0 {
		t.Fatal(issues)
	}
	s.Plan.UpgradeOrders = []string{"arsenal_2", "arsenal"}
	if issues := ValidateSubmission(r, s); len(issues) == 0 {
		t.Fatal("reversed levels accepted")
	}
}
