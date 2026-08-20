package service

import (
	battle "github.com/twh09/autonomous_battlefield/backend/internal/battle/simulation"
	"github.com/twh09/autonomous_battlefield/backend/internal/battle/simulation/controller"
)

type FactionIdentity struct {
	ID   string
	Name string
}

type ScenarioSummary struct {
	ID                    string          `json:"id"`
	RulesVersion          string          `json:"rulesVersion"`
	TickDuration          string          `json:"tickDuration"`
	DecisionIntervalTicks controller.Tick `json:"decisionIntervalTicks"`
	DecisionLeadTicks     controller.Tick `json:"decisionLeadTicks"`
}

// ScenarioCatalog describes the immutable scenario data required by the service.
type ScenarioCatalog interface {
	Resolve(string, [2]FactionIdentity) (battle.Scenario, string, error)
	Summaries() []ScenarioSummary
}
