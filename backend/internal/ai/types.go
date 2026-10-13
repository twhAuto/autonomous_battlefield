// Package ai defines provider-neutral decision contracts. It intentionally
// depends on bounded game views, never on authoritative persistence types.
package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
)

const (
	ControllerScripted = "scripted"
	ControllerModel    = "model"
	ContextSchema      = "battlefield.decision-context.v4"
	PlanSchema         = "battlefield.strategy-plan.v2"
	ToolSchema         = "battlefield.mcp-tools.v7"
)

type DecisionSession struct {
	FactionName     string
	Background      string
	JobType         string
	Doctrine        string
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

// Message is one entry in a provider-neutral conversation. Role is "user",
// "assistant" or "tool". An assistant message carries any text and the tool calls it
// requested; a tool message answers one call, identified by ToolCallID and Name, with
// the result in Content.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"toolCalls,omitempty"`
	ToolCallID string     `json:"toolCallId,omitempty"`
	Name       string     `json:"name,omitempty"`
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
	// Signature is opaque provider state, such as a Gemini thought signature, that
	// must be sent back with the call in later requests of the same conversation.
	Signature string `json:"signature,omitempty"`
}

type Usage struct {
	// Scope identifies whether usage is summed API calls or a CLI session envelope.
	Scope                 string `json:"scope,omitempty" yaml:"scope,omitempty"`
	TokensReported        bool   `json:"tokensReported,omitempty" yaml:"tokensReported,omitempty"`
	CachedInputTokens     *int64 `json:"cachedInputTokens,omitempty" yaml:"cachedInputTokens,omitempty"`
	ReasoningOutputTokens *int64 `json:"reasoningOutputTokens,omitempty" yaml:"reasoningOutputTokens,omitempty"`
	ModelCalls            *int64 `json:"modelCalls,omitempty" yaml:"modelCalls,omitempty"`
	CLIInvocations        int64  `json:"cliInvocations,omitempty" yaml:"cliInvocations,omitempty"`

	InputTokens  int64 `json:"inputTokens,omitempty" yaml:"inputTokens,omitempty"`
	OutputTokens int64 `json:"outputTokens,omitempty" yaml:"outputTokens,omitempty"`
	Requests     int64 `json:"requests,omitempty" yaml:"requests,omitempty"`
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
	UnitReference      *UnitReferenceSnapshot  `json:"unitReference,omitempty"`
	FactionName        string                  `json:"factionName,omitempty"`
	Background         string                  `json:"background,omitempty"`
	Doctrine           string                  `json:"doctrine,omitempty"`
	Opening            bool                    `json:"opening,omitempty"`
	OwnedUpgradeIDs    []string                `json:"ownedUpgradeIds,omitempty"`
	Knowledge          *DecisionKnowledge      `json:"knowledge,omitempty"`
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
	Memory             []FactionMemoryEntry    `json:"memory,omitempty"`
	ActivePlan         controller.PlanProposal `json:"activePlan"`
}

type EconomyContext struct {
	MaximumBuildings        int   `json:"maximumBuildings,omitempty"`
	Gold                    int64 `json:"gold"`
	ProjectedIncome         int64 `json:"projectedIncome"`
	EconomyBuildingCost     int64 `json:"economyBuildingCost"`
	EconomyBuildingIncome   int64 `json:"economyBuildingIncome"`
	CurrentEconomyBuildings int   `json:"currentEconomyBuildings"`
	MaximumEconomyBuildings int   `json:"maximumEconomyBuildings"`
}

type LegalChoices struct {
	Buildings          []game.BuildingType     `json:"buildings,omitempty"`
	RecruitmentSources []string                `json:"recruitmentSources,omitempty"`
	Upgrades           []game.Upgrade          `json:"upgrades,omitempty"`
	ActionKinds        []string                `json:"actionKinds"`
	Units              []game.UnitType         `json:"units"`
	EconomySites       []controller.Coordinate `json:"economySites"`
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
	LeaseExpiresAt      *time.Time
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
	Traceparent         string
	Tracestate          string
	CreatedAt           time.Time
	ResolvedAt          *time.Time
}

type ModelProfile struct {
	AllowedRoles                    []string `json:"-" yaml:"allowedRoles"`
	ID                              string   `json:"id" yaml:"id"`
	DisplayName                     string   `json:"displayName" yaml:"displayName"`
	Provider                        string   `json:"provider" yaml:"provider"`
	Runtime                         string   `json:"runtime" yaml:"runtime"`
	Model                           string   `json:"-" yaml:"model,omitempty"`
	ModelEnvironmentVariable        string   `json:"-" yaml:"modelEnvironmentVariable"`
	CredentialEnvironmentVariable   string   `json:"-" yaml:"credentialEnvironmentVariable"`
	AvailabilityEnvironmentVariable string   `json:"-" yaml:"availabilityEnvironmentVariable"`
	SupportedVersionPrefix          string   `json:"-" yaml:"supportedVersionPrefix"`
	Scope                           string   `json:"scope" yaml:"scope"`
	MaxOutputTokens                 int      `json:"-" yaml:"maxOutputTokens"`
	MaxSteps                        int      `json:"-" yaml:"maxSteps"`
	Enabled                         bool     `json:"-" yaml:"enabled"`
	ModelLabel                      string   `json:"modelLabel" yaml:"-"`
	Available                       bool     `json:"available" yaml:"-"`
	ToolCapability                  bool     `json:"toolCapability" yaml:"-"`
	UnavailableReason               string   `json:"unavailableReason,omitempty" yaml:"-"`
}

type ReferenceIdentity struct {
	RulesetID      string `json:"rulesetId"`
	RulesetVersion string `json:"rulesetVersion"`
	Digest         string `json:"digest"`
}

type UnitReferenceSnapshot struct {
	ReferenceIdentity
	Units []UnitDetail `json:"units"`
}

type UnitDetail struct {
	UnitID string         `json:"unitId"`
	Stats  game.UnitType  `json:"stats"`
	Card   *game.UnitCard `json:"card,omitempty"`
}

type KnowledgeLimits struct {
	MaxUnitCards int `json:"maxUnitCards" yaml:"maxUnitCards"`
	MaxBytes     int `json:"maxBytes" yaml:"maxBytes"`
}

func DefaultKnowledgeLimits() KnowledgeLimits { return KnowledgeLimits{16, 16 << 10} }

func (l KnowledgeLimits) Validate() error {
	if l.MaxUnitCards <= 0 || l.MaxBytes <= 0 {
		return fmt.Errorf("knowledgeRetrieval limits must be positive")
	}
	return nil
}

type RetrievedCard struct {
	game.UnitCard
	Stats game.UnitType `json:"stats"`
}

type DecisionKnowledge struct {
	RulesetID      string          `json:"rulesetId"`
	RulesetVersion string          `json:"rulesetVersion"`
	Digest         string          `json:"digest"`
	Limits         KnowledgeLimits `json:"limits"`
	Mechanics      string          `json:"mechanics,omitempty"`
	Cards          []RetrievedCard `json:"cards"`
	SelectedIDs    []string        `json:"selectedIds"`
	SerializedSize int             `json:"serializedSize"`
	Truncated      bool            `json:"truncated"`
}

// FactionMemoryEntry contains only historical facts from a faction's authorized
// observation and its resolved plan. It never carries nested memory or raw text
// from providers. Status distinguishes accepted plans from engine fallbacks.
type FactionMemoryEntry struct {
	ObservationTick   uint64                  `json:"observationTick"`
	TargetTick        uint64                  `json:"targetTick"`
	Status            string                  `json:"status"`
	Economy           EconomyContext          `json:"economy"`
	OwnUnits          int                     `json:"ownUnits"`
	VisibleEnemyUnits int                     `json:"visibleEnemyUnits"`
	Plan              controller.PlanProposal `json:"plan"`
}
