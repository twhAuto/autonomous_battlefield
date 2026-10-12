// Runs claimed background-generation jobs in scoped model sessions and reports their usage
// or failure to the AI gateway.

package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/prompts"
	"github.com/twh09/autonomous_battlefield/backend/internal/telemetry"
)

func (w *Worker) runGeneration(parent context.Context, job job) {
	parent, span := telemetry.StartDecision(parent, job.RequestID)
	defer span.End()
	duration := time.Duration(job.ExecutionTimeoutSeconds) * time.Second
	if duration <= 0 {
		duration = 2 * time.Minute
	}
	policy, stopPolicy := w.watchPolicy(parent)
	defer stopPolicy()
	ctx, cancel := w.executionContext(policy, job, duration)
	defer cancel()
	result := ai.DecisionResult{}
	category := ""
	metadata := map[string]any{}
	profile, err := w.Profiles.Require(job.ProfileID)
	if err != nil {
		category = "profile_unavailable"
	} else {
		profile.MaxSteps = min(3, max(1, profile.MaxSteps))
		profile.MaxOutputTokens = min(1200, max(1, profile.MaxOutputTokens))
		metadata["provider"] = profile.Provider
		metadata["runtime"] = profile.Runtime
		model := profile.Model
		if profile.Runtime == "api" {
			model = os.Getenv(profile.ModelEnvironmentVariable)
		}
		metadata["modelIdentifier"] = model
		metadata["promptVersion"] = prompts.GenerationPromptVersion
		result, category = w.executeGeneration(ctx, job, profile, duration)
	}
	if ctx.Err() != nil {
		category = "timeout"
	}
	telemetry.AIOutcome(span, category != "", category)
	payload, _ := json.Marshal(map[string]any{"requestId": job.RequestID, "category": category, "usage": result.Usage, "metadata": metadata})
	reportCtx, reportCancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer reportCancel()
	request, err := http.NewRequestWithContext(reportCtx, http.MethodPost, w.serviceURL("/internal/agent/generation-finish"), bytes.NewReader(payload))
	if err != nil {
		return
	}
	request.Header.Set("Authorization", bearerPrefix+w.Secret)
	response, err := w.Client.Do(request)
	if err != nil {
		w.logger().ErrorContext(parent, "background_generation_report_failed", "request_id", job.RequestID)
		return
	}
	defer response.Body.Close()
	w.logger().InfoContext(parent, "background_generation_finished", "request_id", job.RequestID, "category", category, "report_status", response.StatusCode)
}

func (w *Worker) executeGeneration(ctx context.Context, job job, profile ai.ModelProfile, duration time.Duration) (ai.DecisionResult, string) {
	token := job.CapabilityToken
	if token == "" {
		return ai.DecisionResult{}, "capability"
	}
	factory := w.RunnerFactory
	if factory == nil {
		factory = defaultRunner
	}
	executor, err := factory(profile)
	if err != nil {
		return ai.DecisionResult{}, "provider_unavailable"
	}
	result, err := executor.Run(ctx, ai.DecisionSession{JobType: "faction_background", RequestID: job.RequestID, CapabilityToken: token, MCPURL: w.serviceURL("/generation-mcp"), Profile: profile})
	if err == nil && result.Accepted {
		return result, ""
	}
	if result.ErrorCategory != "" {
		return result, result.ErrorCategory
	}
	return result, "execution_error"
}
