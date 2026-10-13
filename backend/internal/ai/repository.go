// Defines durable decision records, repository operations, and errors shared by the
// engine, agents, and AI gateway.

package ai

import (
	"context"
	"errors"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
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
	ListFactionMemory(context.Context, string, int, string, uint64) ([]FactionMemoryEntry, error)
	GetDecision(context.Context, string) (DecisionRecord, error)
	ListWindowDecisions(context.Context, string, int, uint64) ([]DecisionRecord, error)
	SubmitDecisionPlan(context.Context, PlanSubmission) (SubmissionResult, error)
	ResolveDecisionFallback(context.Context, string, int64, controller.PlanProposal, string, string) (bool, error)
	ClaimModelDecision(context.Context, string, []string, time.Duration) (DecisionRecord, error)
	FinishDecisionFailure(context.Context, string, string, Usage, map[string]any) error
	// RecordDecisionValidation counts an invalid submission on an open decision and
	// returns the updated count; a closed decision returns 0.
	RecordDecisionValidation(context.Context, string) (int, error)
	RecordDecisionUsage(context.Context, string, Usage, map[string]any) error
}
