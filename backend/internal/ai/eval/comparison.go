// Compares runner variants against the same frozen observations using isolated submissions
// and bounded repetitions.

package eval

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/mcpserver"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
)

// ComparisonVariant creates a fresh runner per sample; conversation state cannot
// leak between repetitions or variants. Disabling memory is an explicit ablation.
type ComparisonVariant struct {
	ID               string
	Provider         string
	Model            string
	Runtime          string
	WithoutMemory    bool
	WithoutKnowledge bool
	NewRunner        func() ai.DecisionRunner
}

type ComparisonOptions struct {
	KnowledgeRetrieval ai.KnowledgeLimits `json:"knowledgeRetrieval"`
	Repetitions        int                `json:"repetitions"`
	MaxSteps           int                `json:"maxSteps"`
	MaxTokens          int                `json:"maxTokens"`
	Timeout            time.Duration      `json:"timeoutNs"`
}

type ComparisonReport struct {
	SchemaVersion string            `json:"schemaVersion"`
	PromptVersion string            `json:"promptVersion"`
	CorpusVersion string            `json:"corpusVersion"`
	Options       ComparisonOptions `json:"options"`
	Passed        bool              `json:"passed"`
	Variants      []VariantReport   `json:"variants"`
}

type VariantReport struct {
	WithoutKnowledge  bool               `json:"withoutKnowledge"`
	ID                string             `json:"id"`
	Provider          string             `json:"provider"`
	Model             string             `json:"model"`
	Runtime           string             `json:"runtime"`
	WithoutMemory     bool               `json:"withoutMemory"`
	BudgetEnforcement string             `json:"budgetEnforcement"`
	Samples           []ComparisonSample `json:"samples"`
	Metrics           ReportMetrics      `json:"metrics"`
}

type ComparisonSample struct {
	Knowledge *KnowledgeReport `json:"knowledge,omitempty"`
	CaseReport
	Repetition         int    `json:"repetition"`
	ContextHash        string `json:"contextHash"`
	ValidationFailures int    `json:"validationFailures"`
}

// Compare runs matched cases through the production MCP server and validator.
// No production database is used. CLI exit status alone cannot pass a sample.
func (r Runner) Compare(ctx context.Context, cases []Case, variants []ComparisonVariant, options ComparisonOptions) (ComparisonReport, error) {
	if options.KnowledgeRetrieval == (ai.KnowledgeLimits{}) {
		options.KnowledgeRetrieval = ai.DefaultKnowledgeLimits()
	}
	if err := options.KnowledgeRetrieval.Validate(); err != nil {
		return ComparisonReport{}, err
	}
	report := ComparisonReport{SchemaVersion: "battlefield.ai-comparison.v1", PromptVersion: ai.PromptVersion, CorpusVersion: r.CorpusVersion, Options: options, Passed: true}
	if len(cases) == 0 || len(cases) > 20 || len(variants) < 1 || len(variants) > 8 || options.Repetitions < 1 || options.Repetitions > 10 || options.MaxSteps < 1 || options.MaxSteps > 16 || options.MaxTokens < 1 || options.MaxTokens > 65536 || options.Timeout <= 0 || options.Timeout > 5*time.Minute {
		return report, errors.New("comparison requires bounded cases, variants, repetitions, steps, tokens and timeout")
	}

	var err error
	report.Variants, err = comparisonVariantReports(variants)
	if err != nil {
		return report, err
	}
	observations, err := comparisonObservations(cases, options.KnowledgeRetrieval)
	if err != nil {
		return report, err
	}
	for repetition := 0; repetition < options.Repetitions; repetition++ {
		if err := r.compareRepetition(ctx, cases, observations, variants, &report, repetition); err != nil {
			return report, err
		}
	}
	for i := range report.Variants {
		report.Variants[i].Metrics = comparisonMetrics(report.Variants[i].Samples)
	}
	return report, nil
}

func comparisonVariantReports(variants []ComparisonVariant) ([]VariantReport, error) {
	seen := map[string]bool{}
	var reports []VariantReport
	for _, v := range variants {
		if v.ID == "" || seen[v.ID] || v.NewRunner == nil || (v.Runtime != "api" && v.Runtime != "cli") {
			return nil, errors.New("invalid or duplicate comparison variant")
		}
		seen[v.ID] = true
		enforcement := "host turn/token limits and per-sample deadline"
		if v.Runtime == "cli" {
			enforcement = "per-sample deadline and three submission rejections; CLI internal turns/tokens are not host-enforced"
		}
		reports = append(reports, VariantReport{ID: v.ID, Provider: v.Provider, Model: v.Model, Runtime: v.Runtime, WithoutMemory: v.WithoutMemory, WithoutKnowledge: v.WithoutKnowledge, BudgetEnforcement: enforcement})
	}
	return reports, nil
}

// Validate the full corpus before any paid call.
func comparisonObservations(cases []Case, limits ai.KnowledgeLimits) ([]ai.DecisionContext, error) {
	observations := make([]ai.DecisionContext, len(cases))
	for i, c := range cases {
		var envelope struct {
			Context ai.DecisionContext `json:"context"`
		}
		if len(c.ContextJSON) > 64<<10 || json.Unmarshal([]byte(c.ContextJSON), &envelope) != nil || envelope.Context.RequestID != c.RequestID || (envelope.Context.TargetTick == 0 && !envelope.Context.Opening) || envelope.Context.ExpectedVersion != 1 || len(envelope.Context.Legal.ActionKinds) == 0 {
			return nil, fmt.Errorf("case %s needs a complete decision context with version 1", c.ID)
		}
		if c.KnowledgeRuleset != nil {
			if err := game.ValidateRuleset(*c.KnowledgeRuleset); err != nil {
				return nil, err
			}
			envelope.Context.Legal.Units = append([]game.UnitType(nil), c.KnowledgeRuleset.Units...)
			var err error
			envelope.Context.Knowledge, err = ai.RetrieveKnowledge(*c.KnowledgeRuleset, envelope.Context, limits)
			if err != nil {
				return nil, err
			}
		}
		if c.KnowledgeRuleset != nil {
			envelope.Context.UnitReference = ai.FreezeUnitReference(*c.KnowledgeRuleset, envelope.Context)
		}
		observations[i] = envelope.Context
	}
	return observations, nil
}

func (r Runner) compareRepetition(ctx context.Context, cases []Case, observations []ai.DecisionContext, variants []ComparisonVariant, report *ComparisonReport, repetition int) error {
	for ci, c := range cases {
		// Rotate execution order so one provider isn't always first.
		for offset := range variants {
			vi := (offset + repetition) % len(variants)
			if err := ctx.Err(); err != nil {
				return err
			}
			observation := comparisonObservation(observations[ci], variants[vi])
			sample, err := r.compareSample(ctx, c, observation, variants[vi], report.Options, repetition+1)
			if err != nil {
				return err
			}
			report.Variants[vi].Samples = append(report.Variants[vi].Samples, sample)
			report.Passed = report.Passed && sample.Passed
		}
	}
	return nil
}

func comparisonMetrics(samples []ComparisonSample) ReportMetrics {
	entries := make([]CaseReport, 0, len(samples))
	for _, sample := range samples {
		entries = append(entries, sample.CaseReport)
	}
	return reportMetrics(entries)
}

func (r Runner) compareSample(ctx context.Context, c Case, observation ai.DecisionContext, variant ComparisonVariant, options ComparisonOptions, repetition int) (ComparisonSample, error) {
	ctx, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()
	repository := &comparisonRepository{record: ai.DecisionRecord{ID: c.RequestID, Version: 1, TargetTick: observation.TargetTick, Status: "running", Runtime: variant.Runtime, Observation: observation, ProviderMetadata: map[string]any{}}}
	secret := rand.Text()
	reads := &referenceReadCounter{}
	handler := mcpserver.NewWithLogger(repository, secret, nil, slog.New(reads)).Handler()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return ComparisonSample{}, err
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	token, _ := ai.MintCapability(secret, c.RequestID, time.Now().Add(options.Timeout+time.Second))
	started := time.Now()
	result, runErr := variant.NewRunner().Run(ctx, ai.DecisionSession{Doctrine: observation.Doctrine, FactionName: observation.FactionName, Background: observation.Background, RequestID: c.RequestID, MCPURL: "http://" + listener.Addr().String(), CapabilityToken: token, Profile: ai.ModelProfile{MaxSteps: options.MaxSteps, MaxOutputTokens: options.MaxTokens}})
	repository.mu.Lock()
	defer repository.mu.Unlock()
	accepted := repository.record.Status == "accepted"
	category := result.ErrorCategory
	if !accepted && category == "" {
		category = "missing_submission"
	}
	hash, _ := ai.ObservationHash(observation)
	entry := CaseReport{Usage: &result.Usage, ID: c.ID, Category: c.Category, Profile: variant.ID, Model: variant.Model, PromptVersion: ai.PromptVersion, ToolSchemaVersion: ai.ToolSchema, RulesVersion: c.RulesVersion, Passed: accepted && runErr == nil, Accepted: accepted, ErrorCategory: category, DurationMS: time.Since(started).Milliseconds(), InputTokens: result.Usage.InputTokens, OutputTokens: result.Usage.OutputTokens, Requests: result.Usage.Requests, ToolCalls: append([]string(nil), repository.calls...)}
	entry.UnitDetailCalls = int(reads.calls.Load())
	payload, _ := json.Marshal(map[string]any{"context": ai.AgentObservation(observation)})
	entry.ContextBytes = len(payload)
	entry.Outcome = "rejected"
	if accepted {
		entry.Outcome = "accepted"
		// Names, rationales and request metadata do not define tactical diversity.
		plan := *repository.record.ResolvedPlan
		plan.Name = ""
		plan.Rationale = ""
		payload, _ := json.Marshal(plan)
		sum := sha256.Sum256(payload)
		entry.StrategyFingerprint = hex.EncodeToString(sum[:])
	} else {
		entry.FallbackCategory = category
	}
	if !entry.Passed {
		entry.SafetyFailures = []string{"runner did not complete with a validated accepted plan"}
	}
	if cost, ok := r.Prices.Estimate(variant.Model, result.Usage); ok && variant.Runtime == "api" {
		entry.EstimatedCostUSD = &cost
		entry.PricingVersion = r.Prices.Version
	}
	return ComparisonSample{Knowledge: knowledgeReport(observation.Knowledge), CaseReport: entry, Repetition: repetition, ContextHash: hash, ValidationFailures: repository.validationFailures}, nil
}

// Only MCP's decision methods are exposed by this server, never AgentHandler.
type comparisonRepository struct {
	ai.DecisionRepository
	mu                 sync.Mutex
	record             ai.DecisionRecord
	calls              []string
	validationFailures int
}

func (r *comparisonRepository) GetDecision(_ context.Context, id string) (ai.DecisionRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if id != r.record.ID {
		return ai.DecisionRecord{}, ai.ErrDecisionNotFound
	}
	copy := r.record
	copy.ProviderMetadata = map[string]any{"validationFailures": r.validationFailures}
	return copy, nil
}
func (r *comparisonRepository) SubmitDecisionPlan(_ context.Context, s ai.PlanSubmission) (ai.SubmissionResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, "submit_strategy_plan")
	if s.RequestID != r.record.ID || s.ExpectedVersion != r.record.Version || r.record.Status != "running" {
		return ai.SubmissionResult{Status: "rejected"}, nil
	}
	r.record.Status = "accepted"
	r.record.ResolvedPlan = &s.Plan
	r.record.Version++
	return ai.SubmissionResult{Accepted: true, Status: "accepted"}, nil
}
func (r *comparisonRepository) RecordDecisionValidation(context.Context, string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.validationFailures++
	r.calls = append(r.calls, "submit_strategy_plan")
	return nil
}
func (r *comparisonRepository) FinishDecisionFailure(_ context.Context, _ string, category string, _ ai.Usage, _ map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.record.Status = "failed"
	r.record.ErrorCategory = category
	return nil
}

func comparisonObservation(observation ai.DecisionContext, variant ComparisonVariant) ai.DecisionContext {
	observation.SchemaVersion = ai.ContextSchema
	if variant.WithoutKnowledge {
		observation.Knowledge = nil
		if observation.UnitReference != nil {
			clone := *observation.UnitReference
			clone.Units = append([]ai.UnitDetail(nil), clone.Units...)
			for i := range clone.Units {
				clone.Units[i].Card = nil
			}
			observation.UnitReference = &clone
		}
	}
	if variant.WithoutMemory {
		observation.Memory = nil
	}
	return observation
}

// Counts authorized lookup attempts from the real MCP server without retaining payloads.
type referenceReadCounter struct{ calls atomic.Int64 }

func (h *referenceReadCounter) Enabled(context.Context, slog.Level) bool { return true }
func (h *referenceReadCounter) Handle(_ context.Context, r slog.Record) error {
	if r.Message == "unit_details_requested" {
		h.calls.Add(1)
	}
	return nil
}
func (h *referenceReadCounter) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *referenceReadCounter) WithGroup(string) slog.Handler      { return h }
