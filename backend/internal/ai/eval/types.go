// Package eval implements versioned, provider-neutral AI decision evaluations.
// Reports intentionally contain aggregate metadata and never raw observations,
// capabilities, credentials, prompts, or provider payloads.
package eval

import (
	"time"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/game"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai"
)

const (
	CaseSchema   = "battlefield.ai-eval-case.v1"
	ReportSchema = "battlefield.ai-eval-report.v1"
)

type Case struct {
	KnowledgeRuleset *game.Ruleset `yaml:"knowledgeRuleset"`
	SchemaVersion    string        `yaml:"schemaVersion"`
	ID               string        `yaml:"id"`
	Category         string        `yaml:"category"`
	RulesVersion     string        `yaml:"rulesVersion"`
	Profile          string        `yaml:"profile"`
	Model            string        `yaml:"model"`
	RequestID        string        `yaml:"requestId"`
	MaxSteps         int           `yaml:"maxSteps"`
	MaxTokens        int           `yaml:"maxTokens"`
	Capability       string        `yaml:"capability"`
	ContextJSON      string        `yaml:"contextJson"`
	Turns            []ScriptTurn  `yaml:"turns"`
	ToolFailure      bool          `yaml:"toolFailure"`
	Submission       Submission    `yaml:"submission"`
	Expected         Expected      `yaml:"expected"`
}

type ScriptTurn struct {
	Text      string       `yaml:"text"`
	Error     string       `yaml:"error"`
	ToolCalls []ScriptCall `yaml:"toolCalls"`
	Usage     ai.Usage     `yaml:"usage"`
}

type ScriptCall struct {
	ID        string `yaml:"id"`
	Name      string `yaml:"name"`
	Arguments string `yaml:"arguments"`
}

type Submission struct {
	Accepted bool   `yaml:"accepted"`
	Status   string `yaml:"status"`
}

type Expected struct {
	Accepted         bool     `yaml:"accepted"`
	ErrorCategory    string   `yaml:"errorCategory"`
	MaximumToolCalls int      `yaml:"maximumToolCalls"`
	SafetyCritical   bool     `yaml:"safetyCritical"`
	ForbiddenOutput  []string `yaml:"forbiddenOutput"`
}

type Report struct {
	SchemaVersion string            `json:"schemaVersion"`
	CorpusVersion string            `json:"corpusVersion"`
	PromptVersion string            `json:"promptVersion"`
	ToolVersion   string            `json:"toolSchemaVersion"`
	StartedAt     time.Time         `json:"startedAt"`
	DurationMS    int64             `json:"durationMs"`
	Passed        bool              `json:"passed"`
	Cases         []CaseReport      `json:"cases"`
	Metrics       ReportMetrics     `json:"metrics"`
	Baseline      *ReportMetrics    `json:"scriptedBaseline,omitempty"`
	Comparison    *ReportComparison `json:"comparison,omitempty"`
}

type ReportMetrics struct {
	TotalInputTokens  int64    `json:"totalInputTokens"`
	UnitDetailCalls   int      `json:"unitDetailCalls"`
	LookupRate        float64  `json:"lookupRate"`
	CaseCount         int      `json:"caseCount"`
	AcceptedRate      float64  `json:"acceptedRate"`
	InvalidPlanRate   float64  `json:"invalidPlanRate"`
	FallbackRate      float64  `json:"fallbackRate"`
	AverageLatencyMS  float64  `json:"averageLatencyMs"`
	TotalCostUSD      float64  `json:"totalEstimatedCostUsd"`
	StrategyDiversity int      `json:"strategyDiversity"`
	WinRate           *float64 `json:"winRate"`
}

type ReportComparison struct {
	InputTokensDelta      int64    `json:"inputTokensDelta"`
	LookupRateDelta       float64  `json:"lookupRateDelta"`
	AcceptedRateDelta     float64  `json:"acceptedRateDelta"`
	InvalidPlanRateDelta  float64  `json:"invalidPlanRateDelta"`
	FallbackRateDelta     float64  `json:"fallbackRateDelta"`
	AverageLatencyMSDelta float64  `json:"averageLatencyMsDelta"`
	CostDeltaUSD          float64  `json:"estimatedCostDeltaUsd"`
	DiversityDelta        int      `json:"strategyDiversityDelta"`
	WinRateDelta          *float64 `json:"winRateDelta"`
}

type CaseReport struct {
	Usage               *ai.Usage `json:"usage,omitempty"`
	UnitDetailCalls     int       `json:"unitDetailCalls"`
	ContextBytes        int       `json:"contextBytes"`
	ID                  string    `json:"id"`
	Category            string    `json:"category"`
	Profile             string    `json:"profile"`
	Model               string    `json:"model"`
	PromptVersion       string    `json:"promptVersion"`
	ToolSchemaVersion   string    `json:"toolSchemaVersion"`
	RulesVersion        string    `json:"rulesVersion"`
	Passed              bool      `json:"passed"`
	Outcome             string    `json:"outcome"`
	Accepted            bool      `json:"accepted"`
	ErrorCategory       string    `json:"errorCategory,omitempty"`
	FallbackCategory    string    `json:"fallbackCategory,omitempty"`
	DurationMS          int64     `json:"durationMs"`
	InputTokens         int64     `json:"inputTokens"`
	OutputTokens        int64     `json:"outputTokens"`
	Requests            int64     `json:"requests"`
	ToolCalls           []string  `json:"toolCalls,omitempty"`
	SafetyFailures      []string  `json:"safetyFailures,omitempty"`
	EstimatedCostUSD    *float64  `json:"estimatedCostUsd,omitempty"`
	PricingVersion      string    `json:"pricingVersion,omitempty"`
	StrategyFingerprint string    `json:"strategyFingerprintSha256,omitempty"`
}

type PriceBook struct {
	SchemaVersion string       `yaml:"schemaVersion"`
	Version       string       `yaml:"version"`
	Models        []ModelPrice `yaml:"models"`
}

type ModelPrice struct {
	Model               string  `yaml:"model"`
	InputUSDPerMillion  float64 `yaml:"inputUsdPerMillion"`
	OutputUSDPerMillion float64 `yaml:"outputUsdPerMillion"`
}
