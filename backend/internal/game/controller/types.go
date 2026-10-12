// Package controller defines the strategy-plan contract shared by scripted and
// model-backed faction controllers: plan proposals, typed orders, validation and
// cloning. It does not mutate battlefield state and does not call an AI model.
package controller

import "errors"

const (
	maxWeight              = int64(10_000)
	maxTargetBonus         = int64(1_000_000_000)
	maxRationaleCharacters = 600
)

var ErrInvalidPlan = errors.New("invalid strategy plan")

// Tick is the simulation's monotonically increasing logical time.
type Tick uint64

// RecruitmentOrder buys Count units of UnitType, optionally from one building.
type RecruitmentOrder struct {
	SourceBuildingID string `json:"sourceBuildingId,omitempty" yaml:"sourceBuildingId,omitempty"`
	UnitType         string `json:"unitType" yaml:"unitType"`
	Count            int    `json:"count" yaml:"count"`
}

// ConstructionOrder places one building of BuildingType at Position.
type ConstructionOrder struct {
	BuildingType string     `json:"buildingType" yaml:"buildingType"`
	Position     Coordinate `json:"position" yaml:"position"`
}

// PlanProposal is the strategy input supplied by a scripted bot or a model
// controller. Weights are fixed-point multipliers where 1,000 means 1x.
type PlanProposal struct {
	ConstructionOrders *[]ConstructionOrder `json:"constructionOrders,omitempty" yaml:"constructionOrders,omitempty"`

	UpgradeOrders          []string            `json:"upgradeOrders,omitempty" yaml:"upgradeOrders,omitempty"`
	RecruitmentOrders      *[]RecruitmentOrder `json:"recruitmentOrders,omitempty" yaml:"recruitmentOrders,omitempty"`
	Name                   string              `json:"name" yaml:"name"`
	Rationale              string              `json:"rationale,omitempty" yaml:"rationale,omitempty"`
	ActionWeights          map[string]int64    `json:"actionWeights,omitempty" yaml:"actionWeights,omitempty"`
	TargetBonuses          map[string]int64    `json:"targetBonuses,omitempty" yaml:"targetBonuses,omitempty"`
	ReserveGold            int64               `json:"reserveGold,omitempty" yaml:"reserveGold,omitempty"`
	SpendOrder             []string            `json:"spendOrder,omitempty" yaml:"spendOrder,omitempty"`
	ConstructionPriorities []Coordinate        `json:"constructionPriorities,omitempty" yaml:"constructionPriorities,omitempty"`
	RecruitmentPriorities  []string            `json:"recruitmentPriorities,omitempty" yaml:"recruitmentPriorities,omitempty"`
}

// Coordinate is independent of the game package so controller plans remain
// reusable without creating a package import cycle.
type Coordinate struct {
	X int `json:"x" yaml:"x"`
	Y int `json:"y" yaml:"y"`
}
