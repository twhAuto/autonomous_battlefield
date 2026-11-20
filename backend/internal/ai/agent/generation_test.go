// Checks the executor scopes background sessions to generation jobs and report their outcomes.

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai/capability"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai/profile"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai/prompts"
)

func TestGenerationWorkerScopesSessionAndReportsOutcomes(t *testing.T) {
	for _, mode := range []string{"success", "model_failure", "factory_failure", "missing_profile", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			checkGenerationOutcome(t, mode)
		})
	}
}

// generationReport is the generation-finish callback body the executor sends.
type generationReport struct {
	RequestID, Category string
	Usage               ai.Usage
	Metadata            map[string]any
}

func checkGenerationOutcome(t *testing.T, mode string) {
	t.Helper()
	profiles := profile.NewRegistry("cluster", []ai.ModelProfile{{ID: "writer", DisplayName: "Writer", Provider: "openai", Runtime: "api", Scope: "cluster", Enabled: true, MaxSteps: 12, MaxOutputTokens: 4000}})
	var report generationReport
	w := &Executor{MCPURL: "http://mcp/mcp", Secret: "secret", Profiles: profiles, Client: generationFinishClient(t, &report)}
	w.RunnerFactory = generationRunnerFactory(t, mode)
	token, _ := capability.Mint("secret", "generation:draft", time.Now().Add(time.Minute))
	j := job{CapabilityToken: token, RequestID: "draft", ProfileID: "writer", JobType: "faction_background", ExecutionTimeoutSeconds: 120}
	if mode == "missing_profile" {
		j.ProfileID = "unknown"
	}
	ctx := context.Background()
	if mode == "timeout" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		cancel()
	}
	w.runGeneration(ctx, j)
	want := map[string]string{"success": "", "model_failure": "invalid_output", "factory_failure": "provider_unavailable", "missing_profile": "profile_unavailable", "timeout": "timeout"}[mode]
	if report.RequestID != "draft" || report.Category != want {
		t.Fatal(report)
	}
	if mode == "success" && (report.Usage.Requests != 2 || report.Metadata["promptVersion"] != prompts.GenerationVersion) {
		t.Fatal(report)
	}
}

// generationFinishClient records the authenticated generation-finish callback in report.
func generationFinishClient(t *testing.T, report *generationReport) *http.Client {
	return &http.Client{Transport: agentRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/internal/agent/generation-finish" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatal(r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(report); err != nil {
			t.Fatal(err)
		}
		return agentResponse(204, ""), nil
	})}
}

// generationRunnerFactory requires bounded generation profiles and fails as mode asks;
// its runner requires a generation session with a draft-scoped capability.
func generationRunnerFactory(t *testing.T, mode string) func(ai.ModelProfile) (ai.DecisionRunner, error) {
	return func(p ai.ModelProfile) (ai.DecisionRunner, error) {
		if p.MaxSteps != 3 || p.MaxOutputTokens != 1200 {
			t.Fatal("unbounded generation", p)
		}
		if mode == "factory_failure" {
			return nil, errors.New("unavailable")
		}
		return runnerFunc(func(ctx context.Context, s ai.DecisionSession) (ai.DecisionResult, error) {
			checkGenerationSession(t, s)
			if mode == "model_failure" {
				return ai.DecisionResult{ErrorCategory: "invalid_output"}, errors.New("invalid")
			}
			return ai.DecisionResult{Accepted: true, Usage: ai.Usage{Requests: 2}}, nil
		}), nil
	}
}

func checkGenerationSession(t *testing.T, s ai.DecisionSession) {
	if s.JobType != "faction_background" || s.MCPURL != "http://mcp/generation-mcp" || s.Doctrine != "" {
		t.Fatal(s)
	}
	id, err := capability.Verify("secret", s.CapabilityToken, time.Now())
	if err != nil || id != "generation:draft" {
		t.Fatal(id, err)
	}
}
