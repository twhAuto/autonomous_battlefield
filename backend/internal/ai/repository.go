package ai

import (
	"context"
	"errors"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/battle/simulation/controller"
)

var (
	ErrDecisionNotFound = errors.New("decision not found")
	ErrDecisionConflict = errors.New("decision conflict")
	ErrNoDecision       = errors.New("no model decision available")
)

const DecisionReadyChannel = "battlefield_decision_ready"

type CreateDecisionParams struct {
	ID                string
	BattleID          string
	Attempt           int
	FactionID         string
	TargetTick        uint64
	ControllerType    string
	ProfileID         string
	Provider          string
	Runtime           string
	ModelIdentifier   string
	PromptVersion     string
	ToolSchemaVersion string
	Observation       DecisionContext
	ObservationHash   string
	Traceparent       string
	Tracestate        string
}

type DecisionRepository interface {
	CreateOrReuseDecision(context.Context, CreateDecisionParams) (DecisionRecord, error)
	GetDecision(context.Context, string) (DecisionRecord, error)
	ListWindowDecisions(context.Context, string, int, uint64) ([]DecisionRecord, error)
	SubmitDecisionPlan(context.Context, PlanSubmission) (SubmissionResult, error)
	ResolveDecisionFallback(context.Context, string, int64, controller.PlanProposal, string, string) (bool, error)
	ClaimModelDecision(context.Context, string, []string, time.Duration) (DecisionRecord, error)
	FinishDecisionFailure(context.Context, string, string, Usage, map[string]any) error
	RecordDecisionUsage(context.Context, string, Usage, map[string]any) error
}
