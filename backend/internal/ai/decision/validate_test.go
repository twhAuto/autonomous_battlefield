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

func TestModelZeroActionWeightIsRejected(t *testing.T) {
	record := ai.DecisionRecord{Version: 1, TargetTick: 10, Runtime: "api", Observation: ai.DecisionContext{Legal: ai.LegalChoices{ActionKinds: []string{"attack", "defend"}}}}
	submission := ai.PlanSubmission{ExpectedVersion: 1, TargetTick: 10, IdempotencyKey: "once", Plan: controller.PlanProposal{Name: "hold", ActionWeights: map[string]int64{"attack": 0, "defend": 1000}}}
	issues := ValidateSubmission(record, submission)
	if len(issues) != 1 || issues[0].Field != "plan.actionWeights.attack" || issues[0].Code != "invalid" {
		t.Fatalf("zero weight issues = %+v", issues)
	}
	submission.Plan.ActionWeights["attack"] = 1
	if issues := ValidateSubmission(record, submission); len(issues) != 0 {
		t.Fatalf("minimum weight rejected: %+v", issues)
	}
	// The engine still reads a stored or scripted zero as the default weight.
	record.Runtime = "scripted"
	submission.Plan.ActionWeights["attack"] = 0
	if issues := ValidateSubmission(record, submission); len(issues) != 0 {
		t.Fatalf("scripted zero weight rejected: %+v", issues)
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

func TestSpecializedProductionUsesFrozenBuildingRoster(t *testing.T) {
	context := ai.DecisionContext{Legal: ai.LegalChoices{Buildings: []game.BuildingType{{ID: game.BuildingBarracks, Trains: []string{"vanguard"}}}, RecruitmentSources: []string{"a:barracks:1"}}, View: game.View{State: game.State{Buildings: []game.Building{{ID: "a:barracks:1", Trains: []string{"vanguard"}}}}}}
	for _, tc := range []struct {
		unit, source string
		valid        bool
	}{
		{"vanguard", "", true}, {"vanguard", "a:barracks:1", true}, {"ranger", "a:barracks:1", false}, {"vanguard", "a:castle", false}, {"vanguard", "b:barracks:1", false}, {"ranger", "", false},
	} {
		orders := []controller.RecruitmentOrder{{UnitType: tc.unit, SourceBuildingID: tc.source, Count: 1}}
		issues := validateTrainingSources(context, controller.PlanProposal{RecruitmentOrders: &orders})
		if (len(issues) == 0) != tc.valid {
			t.Fatalf("%+v: %+v", tc, issues)
		}
	}
	context.Legal.RecruitmentSources = nil
	orders := []controller.RecruitmentOrder{{UnitType: "vanguard", Count: 1}}
	if len(validateTrainingSources(context, controller.PlanProposal{RecruitmentOrders: &orders})) == 0 {
		t.Fatal("unavailable source accepted")
	}
}

func TestCastleProductionUsesFrozenRoster(t *testing.T) {
	context := ai.DecisionContext{Legal: ai.LegalChoices{RecruitmentSources: []string{"a:castle"}}, View: game.View{State: game.State{Buildings: []game.Building{{ID: "a:castle", Trains: []string{"vanguard", "striker", "pikeman"}}}}}}
	for _, tc := range []struct {
		unit, source string
		valid        bool
	}{
		{"vanguard", "", true}, {"striker", "a:castle", true}, {"pikeman", "a:castle", true}, {"ranger", "a:castle", false}, {"vanguard", "b:castle", false},
	} {
		orders := []controller.RecruitmentOrder{{UnitType: tc.unit, SourceBuildingID: tc.source, Count: 1}}
		if issues := validateTrainingSources(context, controller.PlanProposal{RecruitmentOrders: &orders}); (len(issues) == 0) != tc.valid {
			t.Fatalf("%+v: %+v", tc, issues)
		}
	}
}

func TestConstructionRespectsFrozenBuildingLimits(t *testing.T) {
	sites := []controller.Coordinate{{X: 9, Y: 7}, {X: 9, Y: 9}}
	legal := ai.LegalChoices{ActionKinds: []string{"attack"}, Buildings: []game.BuildingType{{ID: "defense_tower"}, {ID: game.BuildingEconomy}}, EconomySites: sites}
	r := ai.DecisionRecord{Version: 1, TargetTick: 10, Runtime: "cli", Observation: ai.DecisionContext{Legal: legal, Economy: ai.EconomyContext{MaximumBuildings: 8, CurrentBuildings: 7, MaximumEconomyBuildings: 2, CurrentEconomyBuildings: 2}}}
	construction := []controller.ConstructionOrder{{BuildingType: "defense_tower", Position: sites[0]}}
	s := ai.PlanSubmission{ExpectedVersion: 1, TargetTick: 10, IdempotencyKey: "once", Plan: controller.PlanProposal{Name: "fill", ConstructionOrders: &construction}}
	if issues := ValidateSubmission(r, s); len(issues) != 0 {
		t.Fatal("the last free slot was rejected", issues)
	}
	construction = append(construction, controller.ConstructionOrder{BuildingType: game.BuildingEconomy, Position: sites[1]})
	issues := ValidateSubmission(r, s)
	if len(issues) != 2 || issues[0].Code != "limit_reached" || issues[1].Code != "limit_reached" {
		t.Fatalf("orders beyond the building and economy limits accepted: %+v", issues)
	}
	if want := "2 construction orders exceed the 1 remaining building slots (7 of 8 used); submit at most 1 or use []"; issues[0].Message != want {
		t.Fatalf("unclear limit message %q", issues[0].Message)
	}
	r.Runtime = ""
	if issues := ValidateSubmission(r, s); len(issues) != 0 {
		t.Fatal("scripted plans rely on engine skips and must not be rejected", issues)
	}
	r.Runtime, r.Observation.Economy = "cli", ai.EconomyContext{}
	if issues := ValidateSubmission(r, s); len(issues) != 0 {
		t.Fatal("observations without limits must not be rejected", issues)
	}
}

// A rejected site names the list to choose from and its closest legal entries, so a
// model can move the order in one retry.
func TestConstructionRejectionsNameTheListAndNearestSites(t *testing.T) {
	mines := []controller.Coordinate{{X: 20, Y: 10}, {X: 4, Y: 10}, {X: 5, Y: 9}, {X: 12, Y: 8}}
	legal := ai.LegalChoices{ActionKinds: []string{"attack"}, Buildings: []game.BuildingType{{ID: "defense_tower"}, {ID: game.BuildingEconomy}},
		EconomySites: []controller.Coordinate{{X: 9, Y: 7}}, GoldMineSites: mines}
	r := ai.DecisionRecord{Version: 1, TargetTick: 10, Observation: ai.DecisionContext{Legal: legal, View: game.View{State: game.State{GoldMines: []game.GoldMine{{}}}}}}
	construction := []controller.ConstructionOrder{
		{BuildingType: game.BuildingEconomy, Position: controller.Coordinate{X: 4, Y: 9}},
		{BuildingType: "wall", Position: controller.Coordinate{X: 9, Y: 7}},
	}
	s := ai.PlanSubmission{ExpectedVersion: 1, TargetTick: 10, IdempotencyKey: "once", Plan: controller.PlanProposal{Name: "expand", ConstructionOrders: &construction}}
	issues := ValidateSubmission(r, s)
	want := []string{
		`construction "economy" at (4,9) is not in legal.goldMineSites; nearest legal: (5,9), (4,10), (12,8)`,
		`construction "wall" is not in legal.buildings`,
	}
	if len(issues) != len(want) || issues[0].Message != want[0] || issues[1].Message != want[1] {
		t.Fatalf("issues = %+v", issues)
	}
	r.Observation.Legal.GoldMineSites = nil
	construction = construction[:1]
	if issues := ValidateSubmission(r, s); len(issues) != 1 || issues[0].Message != `construction "economy" at (4,9) is not in legal.goldMineSites; none is left, so send []` {
		t.Fatalf("issues with no sites left = %+v", issues)
	}
	r.Observation.Opening = true
	s.Plan.SpendOrder = []string{"construction"}
	issues = ValidateSubmission(r, s)
	messages := map[string]bool{}
	for _, issue := range issues {
		messages[issue.Message] = true
	}
	if !messages["Remove construction from spendOrder: the opening has no construction."] || !messages["construction is unavailable during the opening; send constructionOrders as []"] {
		t.Fatalf("opening issues = %+v", issues)
	}
}
