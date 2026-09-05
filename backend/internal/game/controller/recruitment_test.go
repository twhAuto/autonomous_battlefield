package controller

import (
	"encoding/json"
	"testing"
)

func TestRecruitmentOrderValidationAndClone(t *testing.T) {
	for _, orders := range [][]RecruitmentOrder{{{UnitType: "ranger", Count: 0}}, {{UnitType: "ranger", Count: -1}}, {{UnitType: "ranger", Count: 7}}, {{UnitType: "", Count: 1}}, {{UnitType: "ranger", Count: 4}, {UnitType: "vanguard", Count: 3}}, make([]RecruitmentOrder, 7)} {
		if ValidatePlanProposal(PlanProposal{Name: "buy", RecruitmentOrders: &orders}, []string{"attack"}) == nil {
			t.Fatalf("invalid orders accepted: %v", orders)
		}
	}
	orders := []RecruitmentOrder{{UnitType: "ranger", Count: 2}}
	plan := PlanProposal{Name: "buy", RecruitmentOrders: &orders}
	if err := ValidatePlanProposal(plan, []string{"attack"}); err != nil {
		t.Fatal(err)
	}
	copy := ClonePlanProposal(plan)
	(*copy.RecruitmentOrders)[0].Count = 1
	if orders[0].Count != 2 {
		t.Fatal("shared order slice")
	}
	plan.RecruitmentPriorities = []string{"ranger"}
	if ValidatePlanProposal(plan, []string{"attack"}) == nil {
		t.Fatal("ambiguous buying modes accepted")
	}
	for _, payload := range []string{`{"name":"legacy"}`, `{"name":"none","recruitmentOrders":[]}`} {
		var p PlanProposal
		if err := json.Unmarshal([]byte(payload), &p); err != nil {
			t.Fatal(err)
		}
		copy := ClonePlanProposal(p)
		data, _ := json.Marshal(copy)
		var roundtrip PlanProposal
		_ = json.Unmarshal(data, &roundtrip)
		if (p.RecruitmentOrders == nil) != (roundtrip.RecruitmentOrders == nil) {
			t.Fatal("empty vs absent orders lost")
		}
	}
}
