// Checks the executor scopes background sessions to generation jobs and report their outcomes.

package agent

import (
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/capability"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/profile"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/prompts"
	"github.com/twh09/autonomous_battlefield/backend/internal/testutil"

	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
)

func TestGenerationWorkerScopesSessionAndReportsOutcomes(t *testing.T) {
	for _, mode := range []string{"success", "model_failure", "factory_failure", "missing_profile", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			checkGenerationOutcome(t, mode)
		})
	}
}

func checkGenerationOutcome(t *testing.T, mode string) {
	profiles := profile.NewRegistry("cluster", []ai.ModelProfile{{ID: "writer", DisplayName: "Writer", Provider: "openai", Runtime: "api", Scope: "cluster", Enabled: true, MaxSteps: 12, MaxOutputTokens: 4000}})
	var report struct {
		RequestID, Category string
		Usage               ai.Usage
		Metadata            map[string]any
	}
	w := &Executor{MCPURL: "http://mcp/mcp", Secret: "secret", Profiles: profiles, Client: &http.Client{Transport: agentRoundTrip(func(r *http.Request) (*http.Response, error) {
		testutil.Require(t, !(r.URL.Path != "/internal/agent/generation-finish" || r.Header.Get("Authorization") != "Bearer secret"), func() { t.Fatal(r.URL.Path) })
		{
			err := json.NewDecoder(r.Body).Decode(&report)
			testutil.Require(t, err == nil, func() { t.Fatal(err) })
		}
		return agentResponse(204, ""), nil
	})}}
	w.RunnerFactory = func(p ai.ModelProfile) (ai.DecisionRunner, error) {
		testutil.Require(t, !(p.MaxSteps != 3 || p.MaxOutputTokens != 1200), func() { t.Fatal("unbounded generation", p) })
		if mode == "factory_failure" {
			return nil, errors.New("unavailable")
		}
		return runnerFunc(func(ctx context.Context, s ai.DecisionSession) (ai.DecisionResult, error) {
			testutil.Require(t, !(s.JobType != "faction_background" || s.MCPURL != "http://mcp/generation-mcp" || s.Doctrine != ""), func() { t.Fatal(s) })
			id, err := capability.Verify("secret", s.CapabilityToken, time.Now())
			testutil.Require(t, !(err != nil || id != "generation:draft"), func() { t.Fatal(id, err) })
			if mode == "model_failure" {
				return ai.DecisionResult{ErrorCategory: "invalid_output"}, errors.New("invalid")
			}
			return ai.DecisionResult{Accepted: true, Usage: ai.Usage{Requests: 2}}, nil
		}), nil
	}
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
	testutil.Require(t, !(report.RequestID != "draft" || report.Category != want), func() { t.Fatal(report) })
	testutil.Require(t, !(mode == "success" && (report.Usage.Requests != 2 || report.Metadata["promptVersion"] != prompts.GenerationVersion)), func() { t.Fatal(report) })
}
