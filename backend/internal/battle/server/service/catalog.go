package service

import (
	"github.com/twh09/autonomous_battlefield/backend/internal/battle/simulation/controller"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
)

type FactionIdentity struct {
	ID        string
	Name      string
	RulesetID string
	Blueprint game.Blueprint
}

type ScenarioSummary struct {
	ID                    string          `json:"id"`
	RulesVersion          string          `json:"rulesVersion"`
	TickDuration          string          `json:"tickDuration"`
	DecisionIntervalTicks controller.Tick `json:"decisionIntervalTicks"`
	DecisionLeadTicks     controller.Tick `json:"decisionLeadTicks"`
	RulesetID             string          `json:"rulesetId"`
	MaximumTicks          uint64          `json:"maximumTicks"`
	MapWidth              int             `json:"mapWidth"`
	MapHeight             int             `json:"mapHeight"`
}

// ScenarioCatalog describes the immutable scenario data required by the service.
type ScenarioCatalog interface {
	Resolve(string, [2]FactionIdentity) (game.Scenario, string, error)
	Summaries() []ScenarioSummary
}
