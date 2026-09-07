// Runs claimed background-generation jobs in scoped model sessions and reports their usage
// or failure through MCP.

package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
)

func (w *Worker) runGeneration(parent context.Context, job job) {
	duration := time.Duration(job.ExecutionTimeoutSeconds) * time.Second
	if duration <= 0 {
		duration = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(parent, duration)
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
		metadata["promptVersion"] = ai.GenerationPromptVersion
		token, tokenErr := ai.MintCapability(w.Secret, "generation:"+job.RequestID, time.Now().Add(duration))
		if tokenErr != nil {
			category = "capability"
		} else {
			factory := w.RunnerFactory
			if factory == nil {
				factory = defaultRunner
			}
			executor, runErr := factory(profile)
			if runErr != nil {
				category = "provider_unavailable"
			} else {
				result, runErr = executor.Run(ctx, ai.DecisionSession{JobType: "faction_background", RequestID: job.RequestID, CapabilityToken: token, MCPURL: strings.TrimSuffix(w.MCPURL, "/mcp") + "/generation-mcp", Profile: profile})
				if runErr != nil || !result.Accepted {
					category = result.ErrorCategory
					if category == "" {
						category = "execution_error"
					}
				}
			}
		}
	}
	if ctx.Err() != nil {
		category = "timeout"
	}
	payload, _ := json.Marshal(map[string]any{"requestId": job.RequestID, "category": category, "usage": result.Usage, "metadata": metadata})
	reportCtx, reportCancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer reportCancel()
	request, err := http.NewRequestWithContext(reportCtx, http.MethodPost, strings.TrimSuffix(w.MCPURL, "/mcp")+"/internal/agent/generation-finish", bytes.NewReader(payload))
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
