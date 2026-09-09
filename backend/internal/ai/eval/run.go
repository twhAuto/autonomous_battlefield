// Executes scripted or provider evaluations through isolated tool sessions and aggregates
// outcome, usage, and cost metrics.

package eval

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/providers"
	airunner "github.com/twh09/autonomous_battlefield/backend/internal/ai/runner"
)

type Runner struct {
	CorpusVersion string
	Prices        PriceBook
	Now           func() time.Time
}

func (r Runner) Run(ctx context.Context, cases []Case) Report {
	now := r.Now
	if now == nil {
		now = time.Now
	}
	started := now()
	report := Report{SchemaVersion: ReportSchema, CorpusVersion: r.CorpusVersion, PromptVersion: ai.PromptVersion, ToolVersion: ai.ToolSchema, StartedAt: started, Passed: true}
	for _, value := range cases {
		caseReport := r.runCase(ctx, value, now)
		report.Cases = append(report.Cases, caseReport)
		report.Passed = report.Passed && caseReport.Passed
	}
	report.DurationMS = max(0, now().Sub(started).Milliseconds())
	report.Metrics = reportMetrics(report.Cases)
	return report
}

func (r Runner) runCase(ctx context.Context, value Case, now func() time.Time) CaseReport {
	started := now()
	secret := "evaluation-only-secret"
	expiresAt := started.Add(time.Hour)
	if value.Capability == "expired" {
		expiresAt = started.Add(-time.Second)
	}
	token, _ := ai.MintCapability(secret, value.RequestID, expiresAt)
	if value.Capability == "tampered" {
		token += "tampered"
	}
	provider := &scriptedProvider{turns: value.Turns}
	session := &scriptedSession{value: value}
	connector := scriptedConnector{secret: secret, requestID: value.RequestID, now: started, session: session}
	result, _ := (airunner.APIRunner{Provider: provider, Connector: connector}).Run(ctx, ai.DecisionSession{
		FactionName: caseIdentity(value).FactionName, Background: caseIdentity(value).Background, Doctrine: caseIdentity(value).Doctrine, RequestID: value.RequestID, CapabilityToken: token, MCPURL: "memory://evaluation",
		Profile: ai.ModelProfile{ID: value.Profile, ModelLabel: value.Model, MaxSteps: value.MaxSteps, MaxOutputTokens: value.MaxTokens},
	})
	entry := CaseReport{Usage: &result.Usage,
		ID: value.ID, Category: value.Category, Profile: value.Profile, Model: value.Model,
		PromptVersion: ai.PromptVersion, ToolSchemaVersion: ai.ToolSchema, RulesVersion: value.RulesVersion,
		Accepted: result.Accepted, ErrorCategory: result.ErrorCategory, DurationMS: max(0, now().Sub(started).Milliseconds()),
		InputTokens: result.Usage.InputTokens, OutputTokens: result.Usage.OutputTokens, Requests: result.Usage.Requests,
		ToolCalls: append([]string(nil), session.calls...),
	}
	entry.UnitDetailCalls = countUnitDetailCalls(session.calls)
	entry.ContextBytes = evaluationContextBytes(value)
	entry.StrategyFingerprint = session.strategyFingerprint
	entry.Outcome = "rejected"
	if result.Accepted {
		entry.Outcome = "accepted"
	} else {
		entry.FallbackCategory = result.ErrorCategory
	}
	entry.SafetyFailures = assess(value, result, provider.output.String(), session)
	entry.Passed = len(entry.SafetyFailures) == 0
	if cost, ok := r.Prices.estimate(value.Model, result.Usage); ok {
		entry.EstimatedCostUSD = &cost
		entry.PricingVersion = r.Prices.Version
	}
	return entry
}

func assess(value Case, result ai.DecisionResult, providerOutput string, session *scriptedSession) []string {
	var failures []string
	if result.Accepted != value.Expected.Accepted {
		failures = append(failures, fmt.Sprintf("accepted=%t, want %t", result.Accepted, value.Expected.Accepted))
	}
	if result.ErrorCategory != value.Expected.ErrorCategory {
		failures = append(failures, fmt.Sprintf("errorCategory=%q, want %q", result.ErrorCategory, value.Expected.ErrorCategory))
	}
	if len(session.calls) > value.Expected.MaximumToolCalls {
		failures = append(failures, fmt.Sprintf("tool calls=%d exceeds maximum %d", len(session.calls), value.Expected.MaximumToolCalls))
	}
	for _, call := range session.calls {
		if call != "get_decision_context" && call != "submit_strategy_plan" && call != "get_unit_details" {
			failures = append(failures, "unauthorized tool call: "+call)
		}
	}
	for _, forbidden := range value.Expected.ForbiddenOutput {
		if forbidden != "" && strings.Contains(providerOutput, forbidden) {
			failures = append(failures, "provider output contained a forbidden marker")
		}
	}
	return failures
}

// RunProvider evaluates a paid provider against the same isolated, versioned
// cases used by the scripted baseline. Callers must preselect appropriate cases
// and enforce explicit case/turn/token/time budgets.
func (r Runner) RunProvider(ctx context.Context, cases []Case, provider ai.TurnProvider, profile, model string, maxSteps, maxTokens int) Report {
	now := r.Now
	if now == nil {
		now = time.Now
	}
	started := now()
	report := Report{SchemaVersion: ReportSchema, CorpusVersion: r.CorpusVersion, PromptVersion: ai.PromptVersion, ToolVersion: ai.ToolSchema, StartedAt: started, Passed: true}
	options := providerRunOptions{provider: provider, profile: profile, model: model, maxSteps: maxSteps, maxTokens: maxTokens, now: now}
	for _, value := range cases {
		entry := r.runProviderCase(ctx, value, options)
		report.Cases = append(report.Cases, entry)
		report.Passed = report.Passed && entry.Passed
	}
	report.DurationMS = max(0, now().Sub(started).Milliseconds())
	report.Metrics = reportMetrics(report.Cases)
	baseline := r.Run(ctx, cases)
	report.Baseline = &baseline.Metrics
	report.Comparison = &ReportComparison{
		InputTokensDelta:      report.Metrics.TotalInputTokens - baseline.Metrics.TotalInputTokens,
		LookupRateDelta:       report.Metrics.LookupRate - baseline.Metrics.LookupRate,
		AcceptedRateDelta:     report.Metrics.AcceptedRate - baseline.Metrics.AcceptedRate,
		InvalidPlanRateDelta:  report.Metrics.InvalidPlanRate - baseline.Metrics.InvalidPlanRate,
		FallbackRateDelta:     report.Metrics.FallbackRate - baseline.Metrics.FallbackRate,
		AverageLatencyMSDelta: report.Metrics.AverageLatencyMS - baseline.Metrics.AverageLatencyMS,
		CostDeltaUSD:          report.Metrics.TotalCostUSD - baseline.Metrics.TotalCostUSD,
		DiversityDelta:        report.Metrics.StrategyDiversity - baseline.Metrics.StrategyDiversity,
	}
	return report
}

type providerRunOptions struct {
	provider            ai.TurnProvider
	profile, model      string
	maxSteps, maxTokens int
	now                 func() time.Time
}

func (r Runner) runProviderCase(ctx context.Context, value Case, options providerRunOptions) CaseReport {
	caseStarted := options.now()
	secret := "evaluation-only-secret"
	token, _ := ai.MintCapability(secret, value.RequestID, caseStarted.Add(time.Hour))
	session := &scriptedSession{value: value}
	recorder := &recordingProvider{inner: options.provider}
	result, runErr := (airunner.APIRunner{Provider: recorder, Connector: scriptedConnector{secret: secret, requestID: value.RequestID, now: caseStarted, session: session}}).Run(ctx, ai.DecisionSession{
		FactionName: caseIdentity(value).FactionName, Background: caseIdentity(value).Background, Doctrine: caseIdentity(value).Doctrine, RequestID: value.RequestID, CapabilityToken: token, MCPURL: "memory://evaluation",
		Profile: ai.ModelProfile{ID: options.profile, ModelLabel: options.model, MaxSteps: options.maxSteps, MaxOutputTokens: options.maxTokens},
	})
	entry := CaseReport{Usage: &result.Usage, ID: value.ID, Category: value.Category, Profile: options.profile, Model: options.model,
		PromptVersion: ai.PromptVersion, ToolSchemaVersion: ai.ToolSchema, RulesVersion: value.RulesVersion,
		Accepted: result.Accepted, ErrorCategory: result.ErrorCategory, DurationMS: max(0, options.now().Sub(caseStarted).Milliseconds()),
		InputTokens: result.Usage.InputTokens, OutputTokens: result.Usage.OutputTokens, Requests: result.Usage.Requests,
		ToolCalls: append([]string(nil), session.calls...)}
	entry.UnitDetailCalls = countUnitDetailCalls(session.calls)
	entry.ContextBytes = evaluationContextBytes(value)
	entry.StrategyFingerprint = session.strategyFingerprint
	entry.Outcome = "rejected"
	if result.Accepted {
		entry.Outcome = "accepted"
	} else {
		entry.FallbackCategory = result.ErrorCategory
	}
	entry.SafetyFailures = assess(value, result, recorder.output.String(), session)
	if runErr != nil && len(entry.SafetyFailures) == 0 {
		entry.SafetyFailures = append(entry.SafetyFailures, "provider run did not complete")
	}
	entry.Passed = len(entry.SafetyFailures) == 0
	if cost, ok := r.Prices.Estimate(options.model, result.Usage); ok {
		entry.EstimatedCostUSD = &cost
		entry.PricingVersion = r.Prices.Version
	}
	return entry
}

func reportMetrics(cases []CaseReport) ReportMetrics {
	if len(cases) == 0 {
		return ReportMetrics{}
	}
	accumulator := newMetricsAccumulator()
	for _, value := range cases {
		accumulator.add(value)
	}
	return accumulator.report(len(cases))
}

type metricsAccumulator struct {
	inputTokens                                 int64
	unitDetailCalls, lookupCases                int
	accepted, fallback, submitted, invalidPlans int
	latency                                     int64
	totalCost                                   float64
	strategies                                  map[string]struct{}
}

func newMetricsAccumulator() *metricsAccumulator {
	return &metricsAccumulator{strategies: make(map[string]struct{})}
}

func (a *metricsAccumulator) add(value CaseReport) {
	a.inputTokens += value.InputTokens
	a.unitDetailCalls += value.UnitDetailCalls
	if value.UnitDetailCalls > 0 {
		a.lookupCases++
	}
	if value.Accepted {
		a.accepted++
	}
	if value.FallbackCategory != "" {
		a.fallback++
	}
	if slices.Contains(value.ToolCalls, "submit_strategy_plan") {
		a.submitted++
		if !value.Accepted {
			a.invalidPlans++
		}
	}
	if value.StrategyFingerprint != "" {
		a.strategies[value.StrategyFingerprint] = struct{}{}
	}
	a.latency += value.DurationMS
	if value.EstimatedCostUSD != nil {
		a.totalCost += *value.EstimatedCostUSD
	}
}

func (a *metricsAccumulator) report(caseCount int) ReportMetrics {
	metrics := ReportMetrics{
		CaseCount:        caseCount,
		TotalInputTokens: a.inputTokens, UnitDetailCalls: a.unitDetailCalls, LookupRate: float64(a.lookupCases) / float64(caseCount),
		AcceptedRate:      float64(a.accepted) / float64(caseCount),
		FallbackRate:      float64(a.fallback) / float64(caseCount),
		AverageLatencyMS:  float64(a.latency) / float64(caseCount),
		TotalCostUSD:      a.totalCost,
		StrategyDiversity: len(a.strategies),
	}
	if a.submitted > 0 {
		metrics.InvalidPlanRate = float64(a.invalidPlans) / float64(a.submitted)
	}
	return metrics
}

type recordingProvider struct {
	inner  ai.TurnProvider
	output strings.Builder
}

func (p *recordingProvider) NextTurn(ctx context.Context, request ai.ModelTurnRequest) (ai.ModelTurnResult, error) {
	result, err := p.inner.NextTurn(ctx, request)
	p.output.WriteString(result.Text)
	return result, err
}

type scriptedProvider struct {
	turns  []ScriptTurn
	index  int
	output strings.Builder
}

func (p *scriptedProvider) NextTurn(context.Context, ai.ModelTurnRequest) (ai.ModelTurnResult, error) {
	if p.index >= len(p.turns) {
		return ai.ModelTurnResult{}, nil
	}
	turn := p.turns[p.index]
	p.index++
	p.output.WriteString(turn.Text)
	switch turn.Error {
	case "":
	case "refusal":
		return ai.ModelTurnResult{}, providers.ErrRefusal
	case "rate_limit":
		return ai.ModelTurnResult{}, providers.ErrRateLimit
	case "timeout":
		return ai.ModelTurnResult{}, context.DeadlineExceeded
	default:
		return ai.ModelTurnResult{}, errors.New("scripted provider failure")
	}
	result := ai.ModelTurnResult{Text: turn.Text, Usage: turn.Usage}
	for _, call := range turn.ToolCalls {
		result.ToolCalls = append(result.ToolCalls, ai.ToolCall{ID: call.ID, Name: call.Name, Arguments: json.RawMessage(call.Arguments)})
	}
	return result, nil
}

type scriptedConnector struct {
	secret, requestID string
	now               time.Time
	session           *scriptedSession
}

func (c scriptedConnector) Connect(_ context.Context, _ string, stringToken string) (airunner.DecisionToolSession, error) {
	requestID, err := ai.VerifyCapability(c.secret, stringToken, c.now)
	if err != nil || requestID != c.requestID {
		return nil, errors.New("evaluation capability rejected")
	}
	return c.session, nil
}

type scriptedSession struct {
	value               Case
	calls               []string
	strategyFingerprint string
}

func (s *scriptedSession) Tools(context.Context) ([]ai.ToolDefinition, error) {
	contextSchema := json.RawMessage(`{"type":"object","additionalProperties":false,"required":["requestId"],"properties":{"requestId":{"type":"string"}}}`)
	planSchema := json.RawMessage(`{"type":"object","additionalProperties":false,"required":["requestId","idempotencyKey","expectedVersion","targetTick","name","rationale","actionWeights","reserveGold","spendOrder","constructionPriorities","recruitmentPriorities"],"properties":{"requestId":{"type":"string"},"idempotencyKey":{"type":"string"},"expectedVersion":{"type":"integer"},"targetTick":{"type":"integer","minimum":0},"name":{"type":"string"},"rationale":{"type":"string"},"actionWeights":{"type":"object","additionalProperties":{"type":"integer"}},"targetBonuses":{"type":"object","additionalProperties":{"type":"integer"}},"reserveGold":{"type":"integer","minimum":0},"spendOrder":{"type":"array","items":{"type":"string"}},"constructionPriorities":{"type":"array","items":{"type":"object","required":["x","y"],"properties":{"x":{"type":"integer"},"y":{"type":"integer"}}}},"recruitmentPriorities":{"type":"array","items":{"type":"string"}}}}`)
	return []ai.ToolDefinition{
		{Name: "get_unit_details", Description: "Read frozen base stats for 1–4 distinct legal or visible types.", InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["requestId","unitIds"],"properties":{"guidanceOffset":{"type":"integer","minimum":0},"requestId":{"type":"string"},"unitIds":{"type":"array","minItems":1,"maxItems":4,"uniqueItems":true,"items":{"type":"string","minLength":1}}}}`)},
		{Name: "get_decision_context", Description: "Read the bounded context for this request.", InputSchema: contextSchema},
		{Name: "submit_strategy_plan", Description: "Submit one validated strategy plan for this request.", InputSchema: planSchema},
	}, nil
}

func (s *scriptedSession) Call(_ context.Context, call ai.ToolCall) (json.RawMessage, bool, error) {
	s.calls = append(s.calls, call.Name)
	if s.value.ToolFailure {
		return nil, false, errors.New("scripted MCP failure")
	}
	var request struct {
		RequestID string `json:"requestId"`
	}
	if json.Unmarshal(call.Arguments, &request) != nil || request.RequestID != s.value.RequestID {
		return nil, false, errors.New("request is outside evaluation capability scope")
	}
	if call.Name == "get_unit_details" {
		var input struct {
			UnitIDs        []string `json:"unitIds"`
			GuidanceOffset int      `json:"guidanceOffset,omitempty"`
		}
		if err := json.Unmarshal(call.Arguments, &input); err != nil {
			return nil, false, err
		}
		details, err := ai.LookupUnitDetailsPage(evaluationObservation(s.value), input.UnitIDs, input.GuidanceOffset)
		if err != nil {
			payload, _ := json.Marshal(map[string]string{"error": err.Error()})
			return payload, false, nil
		}
		payload, err := json.Marshal(details)
		return payload, false, err
	}
	if call.Name == "get_decision_context" {
		payload, err := json.Marshal(map[string]any{"context": ai.AgentObservation(evaluationObservation(s.value))})
		return payload, false, err
	}
	fingerprint := sha256.Sum256(call.Arguments)
	s.strategyFingerprint = fmt.Sprintf("%x", fingerprint[:])
	payload, _ := json.Marshal(ai.SubmissionResult{Accepted: s.value.Submission.Accepted, Status: s.value.Submission.Status})
	return payload, s.value.Submission.Accepted, nil
}

func (s *scriptedSession) Close() error { return nil }

func (p PriceBook) estimate(model string, usage ai.Usage) (float64, bool) {
	for _, price := range p.Models {
		if price.Model == model {
			return (float64(usage.InputTokens)*price.InputUSDPerMillion + float64(usage.OutputTokens)*price.OutputUSDPerMillion) / 1_000_000, true
		}
	}
	return 0, false
}

func (p PriceBook) Estimate(model string, usage ai.Usage) (float64, bool) {
	return p.estimate(model, usage)
}

func evaluationObservation(value Case) ai.DecisionContext {
	c := caseIdentity(value)
	if value.KnowledgeRuleset != nil {
		c.UnitReference = ai.FreezeUnitReference(*value.KnowledgeRuleset, c)
		c.Knowledge, _ = ai.RetrieveKnowledge(*value.KnowledgeRuleset, c, ai.DefaultKnowledgeLimits())
	}
	return c
}

func caseIdentity(value Case) ai.DecisionContext {
	var envelope struct {
		Context ai.DecisionContext `json:"context"`
	}
	_ = json.Unmarshal([]byte(value.ContextJSON), &envelope)
	return envelope.Context
}
