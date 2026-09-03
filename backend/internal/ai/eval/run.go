package eval

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
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
	result, _ := (airunner.AgentLoop{Provider: provider, Connector: connector}).Run(ctx, ai.DecisionSession{
		RequestID: value.RequestID, CapabilityToken: token, MCPURL: "memory://evaluation",
		Profile: ai.ModelProfile{ID: value.Profile, ModelLabel: value.Model, MaxSteps: value.MaxSteps, MaxOutputTokens: value.MaxTokens},
	})
	entry := CaseReport{
		ID: value.ID, Category: value.Category, Profile: value.Profile, Model: value.Model,
		PromptVersion: ai.PromptVersion, ToolSchemaVersion: ai.ToolSchema, RulesVersion: value.RulesVersion,
		Accepted: result.Accepted, ErrorCategory: result.ErrorCategory, DurationMS: max(0, now().Sub(started).Milliseconds()),
		InputTokens: result.Usage.InputTokens, OutputTokens: result.Usage.OutputTokens, Requests: result.Usage.Requests,
		ToolCalls: append([]string(nil), session.calls...),
	}
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
		if call != "get_decision_context" && call != "submit_strategy_plan" {
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
	for _, value := range cases {
		entry := r.runProviderCase(ctx, value, provider, profile, model, maxSteps, maxTokens, now)
		report.Cases = append(report.Cases, entry)
		report.Passed = report.Passed && entry.Passed
	}
	report.DurationMS = max(0, now().Sub(started).Milliseconds())
	report.Metrics = reportMetrics(report.Cases)
	baseline := r.Run(ctx, cases)
	report.Baseline = &baseline.Metrics
	report.Comparison = &ReportComparison{
		AcceptedRateDelta:     report.Metrics.AcceptedRate - baseline.Metrics.AcceptedRate,
		InvalidPlanRateDelta:  report.Metrics.InvalidPlanRate - baseline.Metrics.InvalidPlanRate,
		FallbackRateDelta:     report.Metrics.FallbackRate - baseline.Metrics.FallbackRate,
		AverageLatencyMSDelta: report.Metrics.AverageLatencyMS - baseline.Metrics.AverageLatencyMS,
		CostDeltaUSD:          report.Metrics.TotalCostUSD - baseline.Metrics.TotalCostUSD,
		DiversityDelta:        report.Metrics.StrategyDiversity - baseline.Metrics.StrategyDiversity,
	}
	return report
}

func (r Runner) runProviderCase(ctx context.Context, value Case, provider ai.TurnProvider, profile, model string, maxSteps, maxTokens int, now func() time.Time) CaseReport {
	caseStarted := now()
	secret := "evaluation-only-secret"
	token, _ := ai.MintCapability(secret, value.RequestID, caseStarted.Add(time.Hour))
	session := &scriptedSession{value: value}
	recorder := &recordingProvider{inner: provider}
	result, runErr := (airunner.AgentLoop{Provider: recorder, Connector: scriptedConnector{secret: secret, requestID: value.RequestID, now: caseStarted, session: session}}).Run(ctx, ai.DecisionSession{
		RequestID: value.RequestID, CapabilityToken: token, MCPURL: "memory://evaluation",
		Profile: ai.ModelProfile{ID: profile, ModelLabel: model, MaxSteps: maxSteps, MaxOutputTokens: maxTokens},
	})
	entry := CaseReport{ID: value.ID, Category: value.Category, Profile: profile, Model: model,
		PromptVersion: ai.PromptVersion, ToolSchemaVersion: ai.ToolSchema, RulesVersion: value.RulesVersion,
		Accepted: result.Accepted, ErrorCategory: result.ErrorCategory, DurationMS: max(0, now().Sub(caseStarted).Milliseconds()),
		InputTokens: result.Usage.InputTokens, OutputTokens: result.Usage.OutputTokens, Requests: result.Usage.Requests,
		ToolCalls: append([]string(nil), session.calls...)}
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
	if cost, ok := r.Prices.Estimate(model, result.Usage); ok {
		entry.EstimatedCostUSD = &cost
		entry.PricingVersion = r.Prices.Version
	}
	return entry
}

func reportMetrics(cases []CaseReport) ReportMetrics {
	metrics := ReportMetrics{CaseCount: len(cases)}
	if len(cases) == 0 {
		return metrics
	}
	strategies := map[string]struct{}{}
	var accepted, fallback, submitted, invalidPlans int
	var latency int64
	for _, value := range cases {
		if value.Accepted {
			accepted++
		}
		if value.FallbackCategory != "" {
			fallback++
		}
		for _, tool := range value.ToolCalls {
			if tool == "submit_strategy_plan" {
				submitted++
				if !value.Accepted {
					invalidPlans++
				}
				break
			}
		}
		if value.StrategyFingerprint != "" {
			strategies[value.StrategyFingerprint] = struct{}{}
		}
		latency += value.DurationMS
		if value.EstimatedCostUSD != nil {
			metrics.TotalCostUSD += *value.EstimatedCostUSD
		}
	}
	metrics.AcceptedRate = float64(accepted) / float64(len(cases))
	if submitted > 0 {
		metrics.InvalidPlanRate = float64(invalidPlans) / float64(submitted)
	}
	metrics.FallbackRate = float64(fallback) / float64(len(cases))
	metrics.AverageLatencyMS = float64(latency) / float64(len(cases))
	metrics.StrategyDiversity = len(strategies)
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
	if call.Name == "get_decision_context" {
		contextJSON := s.value.ContextJSON
		if contextJSON == "" {
			contextJSON = `{}`
		}
		return json.RawMessage(contextJSON), false, nil
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
