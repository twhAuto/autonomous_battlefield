// Checks production plan validation: required fields, bounded weights and bonuses,
// spend and priority ordering, and the observable rationale limit.

package controller

import (
	"errors"
	"strings"
	"testing"
)

func TestValidatePlanProposalProductionFields(t *testing.T) {
	valid := PlanProposal{
		Name:                   "balanced",
		ActionWeights:          map[string]int64{"attack": 1200},
		TargetBonuses:          map[string]int64{"castle": maxTargetBonus},
		ReserveGold:            25,
		SpendOrder:             []string{"construction", "recruitment", "upgrades"},
		UpgradeOrders:          []string{"forge"},
		ConstructionPriorities: []Coordinate{{X: 1, Y: 2}},
		RecruitmentPriorities:  []string{"vanguard", "ranger"},
	}
	if err := ValidatePlanProposal(valid, []string{"attack", "defend"}); err != nil {
		t.Fatalf("valid production plan rejected: %v", err)
	}

	attackOnly := []string{"attack"}
	tests := []struct {
		name        string
		proposal    PlanProposal
		actionKinds []string
	}{
		{name: "no action kinds", proposal: valid},
		{name: "empty action kind", proposal: valid, actionKinds: []string{""}},
		{name: "duplicate action kind", proposal: valid, actionKinds: []string{"attack", "attack"}},
		{name: "missing name", proposal: PlanProposal{Name: " "}, actionKinds: attackOnly},
		{name: "negative reserve", proposal: PlanProposal{Name: "bad", ReserveGold: -1}, actionKinds: attackOnly},
		{name: "unsupported action kind", proposal: PlanProposal{Name: "bad", ActionWeights: map[string]int64{"teleport": 1000}}, actionKinds: attackOnly},
		{name: "negative action weight", proposal: PlanProposal{Name: "bad", ActionWeights: map[string]int64{"attack": -1}}, actionKinds: attackOnly},
		{name: "oversized action weight", proposal: PlanProposal{Name: "bad", ActionWeights: map[string]int64{"attack": maxWeight + 1}}, actionKinds: attackOnly},
		{name: "empty target bonus key", proposal: PlanProposal{Name: "bad", TargetBonuses: map[string]int64{" ": 1}}, actionKinds: attackOnly},
		{name: "oversized target bonus", proposal: PlanProposal{Name: "bad", TargetBonuses: map[string]int64{"castle": -maxTargetBonus - 1}}, actionKinds: attackOnly},
		{name: "unknown spend kind", proposal: PlanProposal{Name: "bad", SpendOrder: []string{"trade"}}, actionKinds: attackOnly},
		{name: "duplicate spend kind", proposal: PlanProposal{Name: "bad", SpendOrder: []string{"construction", "construction"}}, actionKinds: attackOnly},
		{name: "duplicate upgrade order", proposal: PlanProposal{Name: "bad", UpgradeOrders: []string{"forge", "forge"}}, actionKinds: attackOnly},
		{name: "empty recruitment priority", proposal: PlanProposal{Name: "bad", RecruitmentPriorities: []string{""}}, actionKinds: attackOnly},
		{name: "duplicate recruitment priority", proposal: PlanProposal{Name: "bad", RecruitmentPriorities: []string{"ranger", "ranger"}}, actionKinds: attackOnly},
		{name: "duplicate construction priority", proposal: PlanProposal{Name: "bad", ConstructionPriorities: []Coordinate{{X: 1, Y: 2}, {X: 1, Y: 2}}}, actionKinds: attackOnly},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidatePlanProposal(tt.proposal, tt.actionKinds); !errors.Is(err, ErrInvalidPlan) {
				t.Fatalf("error = %v, want ErrInvalidPlan", err)
			}
		})
	}
}

func TestValidatePlanProposalBoundsObservableRationale(t *testing.T) {
	valid := PlanProposal{Name: "hold", Rationale: "Enemy pressure is high, so preserve gold for reinforcements."}
	if err := ValidatePlanProposal(valid, []string{"attack"}); err != nil {
		t.Fatalf("valid rationale rejected: %v", err)
	}
	invalid := PlanProposal{Name: "hold", Rationale: strings.Repeat("x", maxRationaleCharacters+1)}
	if err := ValidatePlanProposal(invalid, []string{"attack"}); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("oversized rationale error = %v", err)
	}
}

func TestDuplicateListsNameTheirField(t *testing.T) {
	for field, plan := range map[string]PlanProposal{
		"upgrade orders":         {Name: "p", UpgradeOrders: []string{"arsenal", "arsenal"}},
		"recruitment priorities": {Name: "p", RecruitmentPriorities: []string{"ranger", ""}},
	} {
		err := ValidatePlanProposal(plan, []string{"attack"})
		if !errors.Is(err, ErrInvalidPlan) || !strings.Contains(err.Error(), field+" must be unique") || strings.Contains(err.Error(), "recruitment") != (field == "recruitment priorities") {
			t.Fatalf("%s: error = %v", field, err)
		}
	}
}

func TestProductionOrderBounds(t *testing.T) {
	for _, orders := range [][]ProductionOrder{
		{{UnitType: "vanguard", Enabled: true}},
		{{BuildingID: "castle", Enabled: true}},
		{{BuildingID: "castle"}, {BuildingID: "castle"}},
		make([]ProductionOrder, 10),
	} {
		if ValidatePlanProposal(PlanProposal{Name: "invalid", ProductionOrders: &orders}, []string{"attack"}) == nil {
			t.Fatal("invalid production orders accepted", orders)
		}
	}
}
