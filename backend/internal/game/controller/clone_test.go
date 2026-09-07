// Checks plan copies isolate mutable nested fields from the original proposal.

package controller

import (
	"reflect"
	"testing"
)

func TestClonePlanProposalsIsolatesMutableFields(t *testing.T) {
	original := []PlanProposal{{Name: "plan", Rationale: "reason", ReserveGold: 5,
		ActionWeights: map[string]int64{"attack": 1000}, TargetBonuses: map[string]int64{"enemy": 3},
		SpendOrder: []string{"construction"}, ConstructionPriorities: []Coordinate{{X: 1, Y: 2}}, RecruitmentPriorities: []string{"unit"},
	}}
	cloned := ClonePlanProposals(original)
	if !reflect.DeepEqual(cloned, original) {
		t.Fatal("clone changed plan values")
	}
	cloned[0].Name = "changed"
	cloned[0].ActionWeights["attack"] = 0
	cloned[0].TargetBonuses["enemy"] = 0
	cloned[0].SpendOrder[0] = "recruitment"
	cloned[0].ConstructionPriorities[0].X = 9
	cloned[0].RecruitmentPriorities[0] = "other"
	plan := original[0]
	if plan.Name != "plan" || plan.ActionWeights["attack"] != 1000 || plan.TargetBonuses["enemy"] != 3 || plan.SpendOrder[0] != "construction" || plan.ConstructionPriorities[0].X != 1 || plan.RecruitmentPriorities[0] != "unit" {
		t.Fatalf("mutating clone changed source: %#v", plan)
	}
}
