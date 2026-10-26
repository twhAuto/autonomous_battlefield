// Claims compatible model work from the AI gateway within the configured concurrency limit and
// reports execution outcomes.

// Package agent is the model agent's job executor, run by cmd/agent both in the cluster
// (API profiles) and on the CLI host (subscription CLI profiles). It pulls jobs that the
// AI gateway's dispatch API hands out, runs each through the matching runner, and reports
// usage or failure back. WorkerID keeps the claim protocol's workerId name.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/profile"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/providers"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/runner"
	"github.com/twh09/autonomous_battlefield/backend/internal/observability/redact"
	"github.com/twh09/autonomous_battlefield/backend/internal/observability/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"
)

const bearerPrefix = "Bearer "

var errTransientClaim = errors.New("transient agent claim failure")

var errNoAcceptedPlan = errors.New("decision has no accepted plan")

type Executor struct {
	PolicyPolling                            bool
	MCPURL, Secret, WorkerID                 string
	Profiles                                 *profile.Registry
	PollInterval, ClaimWait, DecisionTimeout time.Duration
	Concurrency                              int
	Client                                   *http.Client
	RunnerFactory                            func(ai.ModelProfile) (ai.DecisionRunner, error)
	Logger                                   *slog.Logger
	ready                                    atomic.Bool
	// slots guards draining and active: a claim starts only while not draining, so
	// once Drain returns, active only falls and reaching zero means the worker is idle.
	slots    sync.Mutex
	draining bool
	active   int
}

func (e *Executor) Ready() bool { return e.ready.Load() }

// Status is the worker's drain state: whether it has stopped claiming, how many claims
// or decisions are still in progress, and its configured concurrency.
type Status struct {
	Draining    bool `json:"draining"`
	Active      int  `json:"active"`
	Concurrency int  `json:"concurrency"`
}

func (e *Executor) Status() Status {
	e.slots.Lock()
	defer e.slots.Unlock()
	return Status{Draining: e.draining, Active: e.active, Concurrency: e.Concurrency}
}

// Drain stops new claims. Claims and decisions already in progress finish normally,
// so an operator can restart the worker once Status reports no active work.
func (e *Executor) Drain() {
	e.setDraining(true)
}

// Resume undoes Drain, for example when an upgrade is abandoned.
func (e *Executor) Resume() {
	e.setDraining(false)
}

func (e *Executor) setDraining(draining bool) {
	e.slots.Lock()
	changed := e.draining != draining
	e.draining = draining
	e.slots.Unlock()
	if changed {
		e.logger().Info("agent_worker_draining", "worker_id", e.WorkerID, "draining", draining)
	}
}

// acquire reserves a slot for one claim and the decision it may return.
func (e *Executor) acquire() bool {
	e.slots.Lock()
	defer e.slots.Unlock()
	if e.draining {
		return false
	}
	e.active++
	return true
}

func (e *Executor) release() {
	e.slots.Lock()
	e.active--
	e.slots.Unlock()
}

// serviceURL resolves a path on the AI gateway that serves the configured /mcp
// endpoint, such as the agent dispatch API or the generation MCP endpoint.
func (e *Executor) serviceURL(path string) string {
	return strings.TrimSuffix(strings.TrimRight(e.MCPURL, "/"), "/mcp") + path
}

func (e *Executor) Run(ctx context.Context) error {
	if e.Concurrency < 1 {
		return errors.New("agent concurrency must be at least one")
	}
	if e.Client == nil {
		e.Client = tracing.HTTPClient(nil)
	}
	if e.Profiles == nil {
		return errors.New("agent profile registry is required")
	}
	if e.PollInterval <= 0 {
		e.PollInterval = time.Second
	}
	if e.ClaimWait <= 0 {
		e.ClaimWait = 5 * time.Second
	}
	profileIDs := e.Profiles.AvailableIDs()
	logger := e.logger()
	logger.InfoContext(ctx, "agent_worker_ready", "worker_id", e.WorkerID, "concurrency", e.Concurrency, "profile_ids", profileIDs)
	e.ready.Store(true)
	defer func() {
		e.ready.Store(false)
		logger.Info("agent_worker_stopped", "worker_id", e.WorkerID)
	}()
	group, groupContext := errgroup.WithContext(ctx)
	for range e.Concurrency {
		group.Go(func() error { return e.runClaimLoop(groupContext, profileIDs) })
	}
	return group.Wait()
}

func (e *Executor) runClaimLoop(ctx context.Context, profileIDs []string) error {
	for ctx.Err() == nil {
		if len(profileIDs) == 0 || !e.acquire() {
			if !waitFor(ctx, e.PollInterval) {
				return nil
			}
			continue
		}
		err := e.claimAndRun(ctx, profileIDs)
		e.release()
		if err != nil {
			return err
		}
	}
	return nil
}

func (e *Executor) claimAndRun(ctx context.Context, profileIDs []string) error {
	job, ok, err := e.claim(ctx, profileIDs)
	if err != nil {
		return e.handleClaimError(ctx, err)
	}
	if !ok {
		return nil
	}
	e.logger().InfoContext(ctx, "decision_claimed", "worker_id", e.WorkerID, "request_id", job.RequestID, "profile_id", job.ProfileID)
	// Each claim loop owns one concurrency slot, so a leased decision is
	// always executed immediately instead of waiting behind another job.
	e.runClaimedJob(ctx, job)
	return nil
}

func (e *Executor) handleClaimError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return nil
	}
	e.logger().ErrorContext(ctx, "decision_claim_failed", "worker_id", e.WorkerID, "error", err)
	if !errors.Is(err, errTransientClaim) {
		return err
	}
	waitFor(ctx, e.PollInterval)
	return nil
}

func waitFor(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

type job struct {
	CreatedAt         time.Time  `json:"createdAt"`
	ClaimedAt         *time.Time `json:"claimedAt"`
	ModelIdentifier   string     `json:"modelIdentifier"`
	ReasoningEffort   string     `json:"reasoningEffort"`
	ExecutionDeadline time.Time  `json:"executionDeadline"`
	CapabilityToken   string     `json:"capabilityToken"`
	BattleID          string     `json:"battleId"`
	FactionID         string     `json:"factionId"`
	Attempt           int        `json:"attempt"`
	TargetTick        uint64     `json:"targetTick"`

	FactionName             string `json:"factionName"`
	Background              string `json:"background"`
	JobType                 string `json:"jobType"`
	ExecutionTimeoutSeconds int    `json:"executionTimeoutSeconds"`
	Doctrine                string `json:"doctrine"`
	RequestID               string `json:"requestId"`
	ProfileID               string `json:"profileId"`
	Traceparent             string `json:"traceparent"`
	Tracestate              string `json:"tracestate"`
}

type jobRun struct {
	logger    *slog.Logger
	started   time.Time
	requestID string
	profile   ai.ModelProfile
}

// logFailure includes a bounded, credential-redacted error detail so operators can
// tell provider limits and CLI errors apart without the CLI's full output.
func (run jobRun) logFailure(ctx context.Context, category, outcome string, runErr error) {
	attributes := []any{"provider", run.profile.Provider, "runtime", run.profile.Runtime, "error_category", category, "runner_outcome", outcome, "duration_ms", time.Since(run.started).Milliseconds()}
	if runErr != nil {
		// redact first: a string attribute bypasses the handler's typed-error redaction.
		attributes = append(attributes, "error", runner.CLIFailureDetail(redact.Error(runErr)))
	}
	run.logger.WarnContext(ctx, "decision_runner_failed", attributes...)
}

func (e *Executor) claim(ctx context.Context, profileIDs []string) (job, bool, error) {
	payload, _ := json.Marshal(map[string]any{
		"selectionVersion": 1, "workerId": e.WorkerID, "leaseSeconds": int(e.DecisionTimeout.Seconds()) + 5,
		"waitSeconds": max(1, min(20, int(e.ClaimWait.Seconds()))), "profileIds": profileIDs, "jobTypes": []string{"battle_decision", "faction_background"},
	})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, e.serviceURL("/internal/agent/claim"), bytes.NewReader(payload))
	if err != nil {
		return job{}, false, err
	}
	request.Header.Set("Authorization", bearerPrefix+e.Secret)
	response, err := e.Client.Do(request)
	if err != nil {
		return job{}, false, fmt.Errorf("%w: %w", errTransientClaim, err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNoContent {
		return job{}, false, nil
	}
	if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
		return job{}, false, fmt.Errorf("%w: HTTP %d", errTransientClaim, response.StatusCode)
	}
	if response.StatusCode/100 != 2 {
		return job{}, false, fmt.Errorf("agent claim status %d", response.StatusCode)
	}
	var value job
	if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
		return job{}, false, err
	}
	return value, true, nil
}

func (e *Executor) runJob(parent context.Context, job job) {
	parent = tracing.ContextWithTrace(parent, job.Traceparent, job.Tracestate)
	parent = tracing.WithDecisionMetadata(parent,
		attribute.String("langfuse.session.id", job.BattleID),
		attribute.String("langfuse.trace.metadata.battle_id", job.BattleID),
		attribute.String("langfuse.trace.metadata.faction_id", job.FactionID),
		attribute.Int("langfuse.trace.metadata.attempt", job.Attempt),
		attribute.Int64("langfuse.trace.metadata.target_tick", int64(job.TargetTick)))
	parent, span := tracing.StartDecision(parent, job.RequestID)
	span.SetAttributes(attribute.String("langfuse.trace.metadata.profile_id", job.ProfileID))
	defer span.End()
	started := time.Now()
	if job.ClaimedAt != nil && !job.CreatedAt.IsZero() {
		span.SetAttributes(attribute.Int64("battlefield.queue_wait_ms", job.ClaimedAt.Sub(job.CreatedAt).Milliseconds()))
	}
	logger := e.logger().With("worker_id", e.WorkerID, "request_id", job.RequestID, "profile_id", job.ProfileID)
	profile, err := e.Profiles.Require(job.ProfileID)
	if err != nil {
		e.reportJobFailure(parent, logger, started, job.RequestID, "profile_unavailable", "profile_unavailable", ai.DecisionResult{})
		return
	}
	profile, err = resolveJobProfile(profile, job)
	if err != nil {
		e.reportJobFailure(parent, logger, started, job.RequestID, "model_selection_unavailable", "model_selection_unavailable", ai.DecisionResult{})
		return
	}
	span.SetAttributes(attribute.String("gen_ai.request.model", profile.ModelLabel), attribute.String("battlefield.reasoning_effort", profile.ReasoningEffort))
	logger.InfoContext(parent, "decision_runner_started", "provider", profile.Provider, "runtime", profile.Runtime)
	policy, stopPolicy := e.watchPolicy(parent)
	defer stopPolicy()
	ctx, cancel := e.executionContext(policy, job, e.DecisionTimeout)
	defer cancel()
	token := job.CapabilityToken
	if token == "" {
		e.reportJobFailure(parent, logger, started, job.RequestID, "capability", "capability_error", ai.DecisionResult{})
		return
	}
	factory := e.RunnerFactory
	if factory == nil {
		factory = defaultRunner
	}
	decisionRunner, err := factory(profile)
	if err != nil {
		e.reportJobFailure(parent, logger, started, job.RequestID, "provider_unavailable", "provider_unavailable", ai.DecisionResult{})
		return
	}
	result, err := decisionRunner.Run(ctx, ai.DecisionSession{Doctrine: job.Doctrine, FactionName: job.FactionName, Background: job.Background, RequestID: job.RequestID, CapabilityToken: token, MCPURL: e.MCPURL, Profile: profile})
	if result.ProviderData == nil {
		result.ProviderData = map[string]any{}
	}
	if span.IsRecording() {
		result.ProviderData["aiTraceId"] = tracing.DecisionTraceID(job.RequestID)
	}
	tracing.AIOutcome(span, err != nil || !result.Accepted, result.ErrorCategory)
	e.finishJob(parent, ctx, jobRun{logger: logger, started: started, requestID: job.RequestID, profile: profile}, result, err)
}

// resolveJobProfile preserves API environment models and frozen CLI choices.
func resolveJobProfile(profile ai.ModelProfile, job job) (ai.ModelProfile, error) {
	if profile.Runtime != "cli" || (job.ModelIdentifier == "" && job.ReasoningEffort == "") {
		return profile, nil
	}
	model, _, err := ai.ResolveModelSelection(profile, job.ModelIdentifier, job.ReasoningEffort)
	if err != nil {
		return ai.ModelProfile{}, err
	}
	profile.Model, profile.ModelLabel = model, model
	// Empty effort is the frozen model-default choice, even if configuration changed.
	profile.ReasoningEffort = job.ReasoningEffort
	return profile, nil
}

func (e *Executor) finishJob(parent, runContext context.Context, run jobRun, result ai.DecisionResult, runErr error) {
	if result.ProviderData == nil {
		result.ProviderData = map[string]any{}
	}
	result.ProviderData["runnerDurationMs"] = time.Since(run.started).Milliseconds()
	tracing.AIOutcome(trace.SpanFromContext(parent), runErr != nil || !result.Accepted, result.ErrorCategory)
	if runErr != nil || !result.Accepted {
		category := result.ErrorCategory
		if category == "" {
			category = "runner"
		}
		outcome := runnerOutcome(runContext, runErr, result.Accepted)
		if result.ProviderData == nil {
			result.ProviderData = map[string]any{}
		}
		result.ProviderData["runnerOutcome"] = outcome
		run.logFailure(parent, category, outcome, runErr)
		if reportErr := e.fail(parent, run.requestID, category, result); reportErr != nil {
			run.logger.ErrorContext(parent, "decision_failure_report_failed", "error", reportErr)
		}
		return
	}
	if reportErr := e.reportUsage(parent, run.requestID, result); reportErr != nil {
		if errors.Is(reportErr, errNoAcceptedPlan) {
			result.Accepted = false
			result.ErrorCategory = "plan_not_submitted"
			e.finishJob(parent, runContext, run, result, reportErr)
		} else {
			tracing.AIOutcome(trace.SpanFromContext(parent), true, "usage_report_failed")
			run.logger.ErrorContext(parent, "decision_usage_report_failed", "error", reportErr)
		}
		return
	}
	run.logger.InfoContext(parent, "decision_runner_completed", "provider", run.profile.Provider, "runtime", run.profile.Runtime, "duration_ms", time.Since(run.started).Milliseconds(), "input_tokens", result.Usage.InputTokens, "output_tokens", result.Usage.OutputTokens, "provider_requests", result.Usage.Requests)
}

func (e *Executor) reportJobFailure(ctx context.Context, logger *slog.Logger, started time.Time, requestID, category, outcome string, result ai.DecisionResult) {
	tracing.AIOutcome(trace.SpanFromContext(ctx), true, category)
	logger.WarnContext(ctx, "decision_runner_failed", "error_category", category, "runner_outcome", outcome, "duration_ms", time.Since(started).Milliseconds())
	if err := e.fail(ctx, requestID, category, result); err != nil {
		logger.ErrorContext(ctx, "decision_failure_report_failed", "error", err)
	}
}

func (e *Executor) logger() *slog.Logger {
	if e.Logger != nil {
		return e.Logger
	}
	return slog.Default()
}

func runnerOutcome(ctx context.Context, err error, accepted bool) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return "canceled"
	}
	if err != nil {
		return "execution_error"
	}
	if !accepted {
		return "not_accepted"
	}
	return "completed"
}

func defaultRunner(profile ai.ModelProfile) (ai.DecisionRunner, error) {
	if profile.Runtime == "cli" {
		return runner.CLIRunner{Provider: profile.Provider, Model: profile.Model, ReasoningEffort: profile.ReasoningEffort, PrefetchContext: profile.PrefetchesContext()}, nil
	}
	var provider ai.TurnProvider
	switch profile.Provider {
	case "openai":
		provider = &providers.OpenAI{APIKey: os.Getenv(profile.CredentialEnvironmentVariable), Model: os.Getenv(profile.ModelEnvironmentVariable)}
	case "gemini":
		provider = &providers.Gemini{APIKey: os.Getenv(profile.CredentialEnvironmentVariable), Model: os.Getenv(profile.ModelEnvironmentVariable)}
	default:
		return nil, fmt.Errorf("unsupported provider %q", profile.Provider)
	}
	return runner.APIRunner{Provider: provider}, nil
}

func (e *Executor) reportUsage(ctx context.Context, requestID string, result ai.DecisionResult) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		err = e.reportUsageOnce(ctx, requestID, result)
		if err == nil || errors.Is(err, errNoAcceptedPlan) || ctx.Err() != nil {
			return err
		}
		if attempt < 2 {
			timer := time.NewTimer(time.Duration(attempt+1) * 100 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	return err
}

func (e *Executor) reportUsageOnce(ctx context.Context, requestID string, result ai.DecisionResult) error {
	payload, _ := json.Marshal(map[string]any{"requestId": requestID, "usage": result.Usage, "metadata": result.ProviderData})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, e.serviceURL("/internal/agent/usage"), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", bearerPrefix+e.Secret)
	response, err := e.Client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		if response.StatusCode == http.StatusConflict {
			return errNoAcceptedPlan
		}
		return fmt.Errorf("agent usage status %d", response.StatusCode)
	}
	return nil
}

func (e *Executor) fail(ctx context.Context, requestID, category string, result ai.DecisionResult) error {
	payload, _ := json.Marshal(map[string]any{"requestId": requestID, "category": category, "usage": result.Usage, "metadata": result.ProviderData})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, e.serviceURL("/internal/agent/fail"), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", bearerPrefix+e.Secret)
	response, err := e.Client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return fmt.Errorf("agent failure status %d", response.StatusCode)
	}
	return nil
}

func (e *Executor) runClaimedJob(ctx context.Context, job job) {
	if job.JobType == "faction_background" {
		e.runGeneration(ctx, job)
	} else {
		e.runJob(ctx, job)
	}
}
