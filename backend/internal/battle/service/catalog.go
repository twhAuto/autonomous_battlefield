// Defines the catalog contract and participant/scenario summaries used to resolve
// immutable battle inputs.

package service

import (
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
)

type FactionIdentity struct {
	Background string
	Doctrine   string
	ID         string
	Name       string
	RulesetID  string
	Blueprint  game.Blueprint
}

type ScenarioSummary struct {
	AllowedUnits          []string        `json:"allowedUnits"`
	AllowedUpgrades       []string        `json:"allowedUpgrades"`
	AllowedBuildings      []string        `json:"allowedBuildings"`
	ID                    string          `json:"id"`
	RulesVersion          string          `json:"rulesVersion"`
	TickDuration          string          `json:"tickDuration"`
	DecisionIntervalTicks controller.Tick `json:"decisionIntervalTicks"`
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
