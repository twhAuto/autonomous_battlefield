package worker

import (
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
			profiles := ai.NewProfileRegistry("cluster", []ai.ModelProfile{{ID: "writer", DisplayName: "Writer", Provider: "openai", Runtime: "api", Scope: "cluster", Enabled: true, MaxSteps: 12, MaxOutputTokens: 4000}})
			var report struct {
				RequestID, Category string
				Usage               ai.Usage
				Metadata            map[string]any
			}
			w := &Worker{MCPURL: "http://mcp/mcp", Secret: "secret", Profiles: profiles, Client: &http.Client{Transport: agentRoundTrip(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/internal/agent/generation-finish" || r.Header.Get("Authorization") != "Bearer secret" {
					t.Fatal(r.URL.Path)
				}
				if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
					t.Fatal(err)
				}
				return agentResponse(204, ""), nil
			})}}
			w.RunnerFactory = func(p ai.ModelProfile) (ai.DecisionRunner, error) {
				if p.MaxSteps != 3 || p.MaxOutputTokens != 1200 {
					t.Fatal("unbounded generation", p)
				}
				if mode == "factory_failure" {
					return nil, errors.New("unavailable")
				}
				return runnerFunc(func(ctx context.Context, s ai.DecisionSession) (ai.DecisionResult, error) {
					if s.JobType != "faction_background" || s.MCPURL != "http://mcp/generation-mcp" || s.Doctrine != "" {
						t.Fatal(s)
					}
					id, err := ai.VerifyCapability("secret", s.CapabilityToken, time.Now())
					if err != nil || id != "generation:draft" {
						t.Fatal(id, err)
					}
					if mode == "model_failure" {
						return ai.DecisionResult{ErrorCategory: "invalid_output"}, errors.New("invalid")
					}
					return ai.DecisionResult{Accepted: true, Usage: ai.Usage{Requests: 2}}, nil
				}), nil
			}
			j := job{RequestID: "draft", ProfileID: "writer", JobType: "faction_background", ExecutionTimeoutSeconds: 120}
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
			if mode == "success" && (report.Usage.Requests != 2 || report.Metadata["promptVersion"] != ai.GenerationPromptVersion) {
				t.Fatal(report)
			}
		})
	}
}
