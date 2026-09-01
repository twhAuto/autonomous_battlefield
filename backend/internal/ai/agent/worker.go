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
	"sync/atomic"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/providers"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/runner"
	"golang.org/x/sync/errgroup"
)

type Worker struct {
	MCPURL, Secret, WorkerID                 string
	Profiles                                 *ai.ProfileRegistry
	PollInterval, ClaimWait, DecisionTimeout time.Duration
	Concurrency                              int
	Client                                   *http.Client
	RunnerFactory                            func(ai.ModelProfile) (ai.DecisionRunner, error)
	Logger                                   *slog.Logger
	ready                                    atomic.Bool
}

func (w *Worker) Ready() bool { return w.ready.Load() }

func (w *Worker) Run(ctx context.Context) error {
	if w.Concurrency < 2 {
		return errors.New("agent concurrency must be at least two")
	}
	if w.Client == nil {
		w.Client = http.DefaultClient
	}
	if w.Profiles == nil {
		return errors.New("agent profile registry is required")
	}
	if w.PollInterval <= 0 {
		w.PollInterval = time.Second
	}
	if w.ClaimWait <= 0 {
		w.ClaimWait = 5 * time.Second
	}
	profileIDs := w.Profiles.AvailableIDs()
	logger := w.logger()
	logger.InfoContext(ctx, "agent_worker_ready", "worker_id", w.WorkerID, "concurrency", w.Concurrency, "profile_ids", profileIDs)
	w.ready.Store(true)
	defer func() {
		w.ready.Store(false)
		logger.Info("agent_worker_stopped", "worker_id", w.WorkerID)
	}()
	group, groupContext := errgroup.WithContext(ctx)
	for range w.Concurrency {
		group.Go(func() error { return w.runClaimLoop(groupContext, profileIDs) })
	}
	return group.Wait()
}

func (w *Worker) runClaimLoop(ctx context.Context, profileIDs []string) error {
	for {
		if ctx.Err() != nil {
			return nil
		}
		if len(profileIDs) == 0 {
			if !waitFor(ctx, w.PollInterval) {
				return nil
			}
			continue
		}
		job, ok, err := w.claim(ctx, profileIDs)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			w.logger().ErrorContext(ctx, "decision_claim_failed", "worker_id", w.WorkerID, "error", err)
			return err
		}
		if !ok {
			continue
		}
		w.logger().InfoContext(ctx, "decision_claimed", "worker_id", w.WorkerID, "request_id", job.RequestID, "profile_id", job.ProfileID)
		// Each claim loop owns one concurrency slot, so a leased decision is
		// always executed immediately instead of waiting behind another job.
		w.runJob(ctx, job)
	}
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
	RequestID string `json:"requestId"`
	ProfileID string `json:"profileId"`
}

func (w *Worker) claim(ctx context.Context, profileIDs []string) (job, bool, error) {
	payload, _ := json.Marshal(map[string]any{
		"workerId": w.WorkerID, "leaseSeconds": int(w.DecisionTimeout.Seconds()) + 5,
		"waitSeconds": max(1, min(20, int(w.ClaimWait.Seconds()))), "profileIds": profileIDs,
	})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(w.MCPURL, "/mcp")+"/internal/agent/claim", bytes.NewReader(payload))
	if err != nil {
		return job{}, false, err
	}
	request.Header.Set("Authorization", "Bearer "+w.Secret)
	response, err := w.Client.Do(request)
	if err != nil {
		return job{}, false, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNoContent {
		return job{}, false, nil
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

func (w *Worker) runJob(parent context.Context, job job) {
	started := time.Now()
	logger := w.logger().With("worker_id", w.WorkerID, "request_id", job.RequestID, "profile_id", job.ProfileID)
	profile, err := w.Profiles.Require(job.ProfileID)
	if err != nil {
		logger.WarnContext(parent, "decision_runner_failed", "error_category", "profile_unavailable", "runner_outcome", "profile_unavailable", "duration_ms", time.Since(started).Milliseconds())
		if reportErr := w.fail(parent, job.RequestID, "profile_unavailable", ai.DecisionResult{}); reportErr != nil {
			logger.ErrorContext(parent, "decision_failure_report_failed", "error", reportErr)
		}
		return
	}
	logger.InfoContext(parent, "decision_runner_started", "provider", profile.Provider, "runtime", profile.Runtime)
	ctx, cancel := context.WithTimeout(parent, w.DecisionTimeout)
	defer cancel()
	token, err := ai.MintCapability(w.Secret, job.RequestID, time.Now().Add(w.DecisionTimeout))
	if err != nil {
		logger.WarnContext(parent, "decision_runner_failed", "error_category", "capability", "runner_outcome", "capability_error", "duration_ms", time.Since(started).Milliseconds())
		if reportErr := w.fail(parent, job.RequestID, "capability", ai.DecisionResult{}); reportErr != nil {
			logger.ErrorContext(parent, "decision_failure_report_failed", "error", reportErr)
		}
		return
	}
	factory := w.RunnerFactory
	if factory == nil {
		factory = defaultRunner
	}
	decisionRunner, err := factory(profile)
	if err != nil {
		logger.WarnContext(parent, "decision_runner_failed", "error_category", "provider_unavailable", "runner_outcome", "provider_unavailable", "duration_ms", time.Since(started).Milliseconds())
		if reportErr := w.fail(parent, job.RequestID, "provider_unavailable", ai.DecisionResult{}); reportErr != nil {
			logger.ErrorContext(parent, "decision_failure_report_failed", "error", reportErr)
		}
		return
	}
	result, err := decisionRunner.Run(ctx, ai.DecisionSession{RequestID: job.RequestID, CapabilityToken: token, MCPURL: w.MCPURL, Profile: profile})
	if err != nil || !result.Accepted {
		category := result.ErrorCategory
		if category == "" {
			category = "runner"
		}
		outcome := runnerOutcome(ctx, err, result.Accepted)
		if result.ProviderData == nil {
			result.ProviderData = map[string]any{}
		}
		result.ProviderData["runnerOutcome"] = outcome
		logger.WarnContext(parent, "decision_runner_failed", "provider", profile.Provider, "runtime", profile.Runtime, "error_category", category, "runner_outcome", outcome, "duration_ms", time.Since(started).Milliseconds())
		if reportErr := w.fail(parent, job.RequestID, category, result); reportErr != nil {
			logger.ErrorContext(parent, "decision_failure_report_failed", "error", reportErr)
		}
	} else {
		logger.InfoContext(parent, "decision_runner_completed", "provider", profile.Provider, "runtime", profile.Runtime, "duration_ms", time.Since(started).Milliseconds(), "input_tokens", result.Usage.InputTokens, "output_tokens", result.Usage.OutputTokens, "provider_requests", result.Usage.Requests)
		if reportErr := w.reportUsage(parent, job.RequestID, result); reportErr != nil {
			logger.ErrorContext(parent, "decision_usage_report_failed", "error", reportErr)
		}
	}
}

func (w *Worker) logger() *slog.Logger {
	if w.Logger != nil {
		return w.Logger
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
		return runner.CLIRunner{Provider: profile.Provider, Model: profile.Model}, nil
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
	return runner.AgentLoop{Provider: provider}, nil
}

func (w *Worker) reportUsage(ctx context.Context, requestID string, result ai.DecisionResult) error {
	payload, _ := json.Marshal(map[string]any{"requestId": requestID, "usage": result.Usage, "metadata": result.ProviderData})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(w.MCPURL, "/mcp")+"/internal/agent/usage", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+w.Secret)
	response, err := w.Client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return fmt.Errorf("agent usage status %d", response.StatusCode)
	}
	return nil
}

func (w *Worker) fail(ctx context.Context, requestID, category string, result ai.DecisionResult) error {
	payload, _ := json.Marshal(map[string]any{"requestId": requestID, "category": category, "usage": result.Usage, "metadata": result.ProviderData})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(w.MCPURL, "/mcp")+"/internal/agent/fail", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+w.Secret)
	response, err := w.Client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return fmt.Errorf("agent failure status %d", response.StatusCode)
	}
	return nil
}
