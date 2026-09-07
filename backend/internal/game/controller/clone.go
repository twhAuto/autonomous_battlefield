// Copies controller plans and their nested collections to prevent callers from mutating
// accepted decisions.

package controller

// ClonePlanProposal copies a proposal without sharing mutable maps or slices.
func ClonePlanProposal(value PlanProposal) PlanProposal {
	result := PlanProposal{UpgradeOrders: append([]string(nil), value.UpgradeOrders...), RecruitmentOrders: CloneRecruitmentOrders(value.RecruitmentOrders), Name: value.Name, Rationale: value.Rationale, ActionWeights: make(map[string]int64, len(value.ActionWeights)), TargetBonuses: make(map[string]int64, len(value.TargetBonuses)), ReserveGold: value.ReserveGold,
		SpendOrder: append([]string(nil), value.SpendOrder...), ConstructionPriorities: append([]Coordinate(nil), value.ConstructionPriorities...), RecruitmentPriorities: append([]string(nil), value.RecruitmentPriorities...)}
	for k, v := range value.ActionWeights {
		result.ActionWeights[k] = v
	}
	for k, v := range value.TargetBonuses {
		result.TargetBonuses[k] = v
	}
	return result
}

// ClonePlanProposals returns an independent copy of each proposal.
func ClonePlanProposals(values []PlanProposal) []PlanProposal {
	result := make([]PlanProposal, len(values))
	for i := range values {
		result[i] = ClonePlanProposal(values[i])
	}
	return result
}

// Preserve absent orders (legacy priorities) versus an explicit empty list (no purchases).
func CloneRecruitmentOrders(orders *[]RecruitmentOrder) *[]RecruitmentOrder {
	if orders == nil {
		return nil
	}
	result := append([]RecruitmentOrder{}, (*orders)...)
	return &result
}
