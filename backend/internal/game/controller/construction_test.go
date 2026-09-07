package controller

import "testing"

func TestTypedConstructionValidationAndClone(t *testing.T) {
	orders := []ConstructionOrder{{BuildingType: "watchtower", Position: Coordinate{X: 4, Y: 3}}}
	plan := PlanProposal{Name: "observe", ConstructionOrders: &orders}
	if err := ValidatePlanProposal(plan, []string{"attack"}); err != nil {
		t.Fatal(err)
	}
	copy := ClonePlanProposal(plan)
	(*copy.ConstructionOrders)[0].Position.X = 99
	if orders[0].Position.X != 4 {
		t.Fatal("construction orders aliased")
	}
	for _, invalid := range []PlanProposal{
		{Name: "mixed", ConstructionOrders: &orders, ConstructionPriorities: []Coordinate{{X: 1, Y: 1}}},
		{Name: "duplicate", ConstructionOrders: &[]ConstructionOrder{orders[0], orders[0]}},
		{Name: "missing", ConstructionOrders: &[]ConstructionOrder{{}}},
		{Name: "too-many", ConstructionOrders: &[]ConstructionOrder{{}, {}, {}, {}, {}, {}, {}, {}, {}}},
	} {
		if ValidatePlanProposal(invalid, []string{"attack"}) == nil {
			t.Fatal("invalid construction accepted")
		}
	}
	empty := []ConstructionOrder{}
	if clone := CloneConstructionOrders(&empty); clone == nil || len(*clone) != 0 {
		t.Fatal("empty construction lost")
	}
}
