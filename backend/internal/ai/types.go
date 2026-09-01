// Package ai defines provider-neutral decision contracts. It intentionally
// depends on bounded game views, never on authoritative persistence types.
package ai

import (
	"context"
	"encoding/json"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/battle/simulation/controller"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
)

const (
	ControllerScripted = "scripted"
	ControllerModel    = "model"
	ContextSchema      = "battlefield.decision-context.v1"
	PlanSchema         = "battlefield.strategy-plan.v1"
	ToolSchema         = "battlefield.mcp-tools.v2"
)

type DecisionSession struct {
	RequestID       string
	CapabilityToken string
	MCPURL          string
	Profile         ModelProfile
}

type DecisionResult struct {
	Accepted      bool
	ErrorCategory string
	Usage         Usage
	ProviderData  map[string]any
}

type ModelTurnRequest struct {
	SystemPrompt string
	Messages     []Message
	Tools        []ToolDefinition
	MaxTokens    int
}

type ModelTurnResult struct {
	Text      string
	ToolCalls []ToolCall
	Usage     Usage
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type Usage struct {
	InputTokens  int64 `json:"inputTokens,omitempty"`
	OutputTokens int64 `json:"outputTokens,omitempty"`
	Requests     int64 `json:"requests,omitempty"`
}

// DecisionRunner is implemented by the shared API agent loop and CLI-owned
// loops. The engine only schedules durable records; it never calls a model.
type DecisionRunner interface {
	Run(context.Context, DecisionSession) (DecisionResult, error)
}

// TurnProvider is the narrow extension point for APIs whose tool loop is
// driven by the shared agent runner.
type TurnProvider interface {
	NextTurn(context.Context, ModelTurnRequest) (ModelTurnResult, error)
}

type DecisionContext struct {
	SchemaVersion      string                  `json:"schemaVersion"`
	RequestID          string                  `json:"requestId"`
	BattleID           string                  `json:"battleId"`
	FactionID          string                  `json:"factionId"`
	TargetTick         uint64                  `json:"targetTick"`
	ExpectedVersion    int64                   `json:"expectedVersion"`
	ObservationVersion uint64                  `json:"observationVersion"`
	ObservationHash    string                  `json:"observationHash"`
	View               game.View               `json:"view"`
	Economy            EconomyContext          `json:"economy"`
	Legal              LegalChoices            `json:"legal"`
	ActivePlan         controller.PlanProposal `json:"activePlan"`
}

type EconomyContext struct {
	Gold                    int64 `json:"gold"`
	ProjectedIncome         int64 `json:"projectedIncome"`
	EconomyBuildingCost     int64 `json:"economyBuildingCost"`
	EconomyBuildingIncome   int64 `json:"economyBuildingIncome"`
	CurrentEconomyBuildings int   `json:"currentEconomyBuildings"`
	MaximumEconomyBuildings int   `json:"maximumEconomyBuildings"`
}

type LegalChoices struct {
	ActionKinds  []string                `json:"actionKinds"`
	Units        []game.UnitType         `json:"units"`
	EconomySites []controller.Coordinate `json:"economySites"`
}

type PlanSubmission struct {
	RequestID       string                  `json:"requestId"`
	IdempotencyKey  string                  `json:"idempotencyKey"`
	ExpectedVersion int64                   `json:"expectedVersion"`
	TargetTick      uint64                  `json:"targetTick"`
	Plan            controller.PlanProposal `json:"plan"`
}

type ValidationIssue struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type SubmissionResult struct {
	Accepted bool              `json:"accepted"`
	Status   string            `json:"status"`
	Errors   []ValidationIssue `json:"errors,omitempty"`
}

type DecisionRecord struct {
	ID                  string
	BattleID            string
	Attempt             int
	FactionID           string
	TargetTick          uint64
	Version             int64
	Status              string
	ControllerType      string
	ProfileID           string
	Provider            string
	Runtime             string
	ModelIdentifier     string
	PromptVersion       string
	ToolSchemaVersion   string
	Observation         DecisionContext
	ObservationHash     string
	ResolvedPlan        *controller.PlanProposal
	FallbackSource      string
	ErrorCategory       string
	Usage               Usage
	ProviderMetadata    map[string]any
	IdempotencyKey      string
	CapabilityHash      string
	CapabilityExpiresAt time.Time
	CreatedAt           time.Time
	ResolvedAt          *time.Time
}

type ModelProfile struct {
	ID                              string `json:"id" yaml:"id"`
	DisplayName                     string `json:"displayName" yaml:"displayName"`
	Provider                        string `json:"provider" yaml:"provider"`
	Runtime                         string `json:"runtime" yaml:"runtime"`
	Model                           string `json:"-" yaml:"model,omitempty"`
	ModelEnvironmentVariable        string `json:"-" yaml:"modelEnvironmentVariable"`
	CredentialEnvironmentVariable   string `json:"-" yaml:"credentialEnvironmentVariable"`
	AvailabilityEnvironmentVariable string `json:"-" yaml:"availabilityEnvironmentVariable"`
	SupportedVersionPrefix          string `json:"-" yaml:"supportedVersionPrefix"`
	Scope                           string `json:"scope" yaml:"scope"`
	MaxOutputTokens                 int    `json:"-" yaml:"maxOutputTokens"`
	MaxSteps                        int    `json:"-" yaml:"maxSteps"`
	Enabled                         bool   `json:"-" yaml:"enabled"`
	ModelLabel                      string `json:"modelLabel" yaml:"-"`
	Available                       bool   `json:"available" yaml:"-"`
	ToolCapability                  bool   `json:"toolCapability" yaml:"-"`
	UnavailableReason               string `json:"unavailableReason,omitempty" yaml:"-"`
}
