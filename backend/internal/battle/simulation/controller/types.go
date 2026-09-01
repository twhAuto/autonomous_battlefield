// Package controller contains deterministic faction-controller policies.
//
// Controllers consume engine-produced action candidates and select one action
// per tick. They do not mutate battlefield state and do not call an AI model.
package controller

import "errors"

const (
	maxBaseUtility         = int64(1_000_000)
	maxWeight              = int64(10_000)
	maxTargetBonus         = int64(1_000_000_000)
	maxRationaleCharacters = 600
)

var (
	ErrInvalidConfig      = errors.New("invalid controller config")
	ErrInvalidObservation = errors.New("invalid controller observation")
	ErrInvalidPlan        = errors.New("invalid strategy plan")
	ErrRequestNotFound    = errors.New("decision request not found")
	ErrRequestExpired     = errors.New("decision request expired")
)

// Tick is the simulation's monotonically increasing logical time.
type Tick uint64

// Candidate is a valid action supplied by the authoritative game engine.
// BaseUtility is calculated from current tactical conditions. Kind and
// TargetID are data-driven identifiers from the active ruleset.
type Candidate struct {
	ID          string `json:"id" yaml:"id"`
	Kind        string `json:"kind" yaml:"kind"`
	TargetID    string `json:"targetId" yaml:"targetId"`
	BaseUtility int64  `json:"baseUtility" yaml:"baseUtility"`
}

// Observation is the bounded faction-visible input to a controller.
type Observation struct {
	BattlefieldID string
	FactionID     string
	Tick          Tick
	Version       uint64
	Candidates    []Candidate
}

// PlanProposal is the strategy input supplied by a human, scripted bot, or
// later an LLM. Weights are fixed-point multipliers where 1,000 means 1x.
// Missing action kinds use Config.DefaultActionWeight.
type PlanProposal struct {
	Name                   string           `json:"name" yaml:"name"`
	Rationale              string           `json:"rationale,omitempty" yaml:"rationale,omitempty"`
	ActionWeights          map[string]int64 `json:"actionWeights,omitempty" yaml:"actionWeights,omitempty"`
	TargetBonuses          map[string]int64 `json:"targetBonuses,omitempty" yaml:"targetBonuses,omitempty"`
	ReserveGold            int64            `json:"reserveGold,omitempty" yaml:"reserveGold,omitempty"`
	SpendOrder             []string         `json:"spendOrder,omitempty" yaml:"spendOrder,omitempty"`
	ConstructionPriorities []Coordinate     `json:"constructionPriorities,omitempty" yaml:"constructionPriorities,omitempty"`
	RecruitmentPriorities  []string         `json:"recruitmentPriorities,omitempty" yaml:"recruitmentPriorities,omitempty"`
}

// Coordinate is independent of the game package so controller plans remain
// reusable without creating a package import cycle.
type Coordinate struct {
	X int `json:"x" yaml:"x"`
	Y int `json:"y" yaml:"y"`
}

// StrategyPlan is an accepted, immutable plan with a deterministic lifetime.
// ValidUntilTick is exclusive.
type StrategyPlan struct {
	ID                     string
	Name                   string
	Rationale              string
	ObservedTick           Tick
	ValidFromTick          Tick
	ValidUntilTick         Tick
	ActionWeights          map[string]int64
	TargetBonuses          map[string]int64
	ReserveGold            int64
	SpendOrder             []string
	ConstructionPriorities []Coordinate
	RecruitmentPriorities  []string
}

// Trait changes tactical preferences without requiring a named branch in the
// controller. Modifiers are added to the active plan's action weights.
type Trait struct {
	ID                    string           `json:"id" yaml:"id"`
	ActionWeightModifiers map[string]int64 `json:"actionWeightModifiers" yaml:"actionWeightModifiers"`
}

// Config controls decision scheduling and the deterministic tactical policy.
type Config struct {
	DecisionIntervalTicks Tick
	DecisionLeadTicks     Tick
	FirstDecisionTick     Tick
	DefaultActionWeight   int64
	ActionKinds           []string
	FallbackPlan          PlanProposal
	Traits                []Trait
}

// DecisionRequest is a replayable snapshot for a strategic decision session.
// A provider may answer asynchronously before SubmitBeforeTick. The accepted
// plan then covers [ApplyAtTick, PlanValidUntilTick).
type DecisionRequest struct {
	ID                 string
	Observation        Observation
	SubmitBeforeTick   Tick
	ApplyAtTick        Tick
	PlanValidUntilTick Tick
}

// Selection identifies the candidate chosen by the tactical policy.
type Selection struct {
	CandidateID     string
	ExpectedVersion uint64
	Score           int64
}

// TickResult contains both the immediate tactical choice and, when due, a
// request for a new strategic plan. UsedFallback reports whether no accepted
// plan covered this tick.
type TickResult struct {
	Selection       *Selection
	DecisionRequest *DecisionRequest
	PlanID          string
	UsedFallback    bool
}

// Controller is the common contract used by scripted and future AI-backed
// faction controllers.
type Controller interface {
	Tick(Observation) (TickResult, error)
	SubmitPlan(requestID string, proposal PlanProposal) (StrategyPlan, error)
}
