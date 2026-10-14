// Runs claimed background-generation jobs in scoped model sessions and reports their usage
// or failure to the AI gateway.

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/prompts"
	"github.com/twh09/autonomous_battlefield/backend/internal/observability/tracing"
)

func (e *Executor) runGeneration(parent context.Context, job job) {
	parent, span := tracing.StartDecision(parent, job.RequestID)
	defer span.End()
	duration := time.Duration(job.ExecutionTimeoutSeconds) * time.Second
	if duration <= 0 {
		duration = 2 * time.Minute
	}
	policy, stopPolicy := e.watchPolicy(parent)
	defer stopPolicy()
	ctx, cancel := e.executionContext(policy, job, duration)
	defer cancel()
	result := ai.DecisionResult{}
	category := ""
	metadata := map[string]any{}
	profile, err := e.Profiles.Require(job.ProfileID)
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
		metadata["promptVersion"] = prompts.GenerationVersion
		result, category = e.executeGeneration(ctx, job, profile, duration)
	}
	if ctx.Err() != nil {
		category = "timeout"
	}
	tracing.AIOutcome(span, category != "", category)
	payload, _ := json.Marshal(map[string]any{"requestId": job.RequestID, "category": category, "usage": result.Usage, "metadata": metadata})
	reportCtx, reportCancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer reportCancel()
	request, err := http.NewRequestWithContext(reportCtx, http.MethodPost, e.serviceURL("/internal/agent/generation-finish"), bytes.NewReader(payload))
	if err != nil {
		return
	}
	request.Header.Set("Authorization", bearerPrefix+e.Secret)
	response, err := e.Client.Do(request)
	if err != nil {
		e.logger().ErrorContext(parent, "background_generation_report_failed", "request_id", job.RequestID)
		return
	}
	defer response.Body.Close()
	e.logger().InfoContext(parent, "background_generation_finished", "request_id", job.RequestID, "category", category, "report_status", response.StatusCode)
}

func (e *Executor) executeGeneration(ctx context.Context, job job, profile ai.ModelProfile, duration time.Duration) (ai.DecisionResult, string) {
	token := job.CapabilityToken
	if token == "" {
		return ai.DecisionResult{}, "capability"
	}
	factory := e.RunnerFactory
	if factory == nil {
		factory = defaultRunner
	}
	executor, err := factory(profile)
	if err != nil {
		return ai.DecisionResult{}, "provider_unavailable"
	}
	result, err := executor.Run(ctx, ai.DecisionSession{JobType: "faction_background", RequestID: job.RequestID, CapabilityToken: token, MCPURL: e.serviceURL("/generation-mcp"), Profile: profile})
	if err == nil && result.Accepted {
		return result, ""
	}
	if result.ErrorCategory != "" {
		return result, result.ErrorCategory
	}
	return result, "execution_error"
}
