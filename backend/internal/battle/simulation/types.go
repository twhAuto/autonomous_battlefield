// Package simulation implements the deterministic battlefield rules.
//
// It deliberately has no HTTP, database, filesystem, or process-lifecycle
// dependencies. Callers provide a frozen scenario and persist returned events.
package simulation

import (
	"errors"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/battle/simulation/controller"
)

const (
	ActionAttack = "attack"
	ActionDefend = "defend"
)

var (
	ErrInvalidBattle     = errors.New("invalid battle")
	ErrBattleFinished    = errors.New("battle already finished")
	ErrBattleNotFinished = errors.New("battle not finished")
)

type CombatRules struct {
	MinimumDamage            int64 `json:"minimumDamage" yaml:"minimumDamage"`
	DefendAttackPercent      int64 `json:"defendAttackPercent" yaml:"defendAttackPercent"`
	DefendDamageTakenPercent int64 `json:"defendDamageTakenPercent" yaml:"defendDamageTakenPercent"`
}

type UnitTemplate struct {
	ID      string `json:"id" yaml:"id"`
	Health  int64  `json:"health" yaml:"health"`
	Attack  int64  `json:"attack" yaml:"attack"`
	Defense int64  `json:"defense" yaml:"defense"`
	Speed   int64  `json:"speed" yaml:"speed"`
}

type FactionSetup struct {
	ID            string                    `json:"id"`
	Name          string                    `json:"name"`
	Units         []UnitTemplate            `json:"units"`
	Traits        []controller.Trait        `json:"traits"`
	FallbackPlan  controller.PlanProposal   `json:"fallbackPlan"`
	ScriptedPlans []controller.PlanProposal `json:"scriptedPlans"`
}

// Scenario is the complete immutable input persisted with a queued battle.
type Scenario struct {
	ID                    string          `json:"id"`
	RulesVersion          string          `json:"rulesVersion"`
	TickDuration          time.Duration   `json:"tickDuration"`
	DecisionIntervalTicks controller.Tick `json:"decisionIntervalTicks"`
	DecisionLeadTicks     controller.Tick `json:"decisionLeadTicks"`
	Rules                 CombatRules     `json:"rules"`
	Factions              []FactionSetup  `json:"factions"`
}

type Unit struct {
	ID        string `json:"id"`
	FactionID string `json:"factionId"`
	Health    int64  `json:"health"`
	Attack    int64  `json:"attack"`
	Defense   int64  `json:"defense"`
	Speed     int64  `json:"speed"`
}

type State struct {
	BattleID string `json:"battleId"`
	Tick     uint64 `json:"tick"`
	Units    []Unit `json:"units"`
}

type Event struct {
	Sequence uint64         `json:"sequence"`
	Tick     uint64         `json:"tick"`
	Type     string         `json:"type"`
	Data     map[string]any `json:"data"`
}

type FactionStats struct {
	FactionID      string `json:"factionId"`
	UnitsStarted   int    `json:"unitsStarted"`
	UnitsRemaining int    `json:"unitsRemaining"`
	DamageDealt    int64  `json:"damageDealt"`
	DamageTaken    int64  `json:"damageTaken"`
	AttackTicks    uint64 `json:"attackTicks"`
	DefendTicks    uint64 `json:"defendTicks"`
}

type Result struct {
	WinnerFactionID *string        `json:"winnerFactionId"`
	Reason          string         `json:"reason"`
	TicksRun        uint64         `json:"ticksRun"`
	Factions        []FactionStats `json:"factions"`
}

type Transition struct {
	Tick     uint64  `json:"tick"`
	Events   []Event `json:"events"`
	Finished bool    `json:"finished"`
	Result   *Result `json:"result,omitempty"`
}
