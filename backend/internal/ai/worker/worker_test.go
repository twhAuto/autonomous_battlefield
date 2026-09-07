// Checks model job claiming, concurrency limits, runner selection, cancellation outcomes,
// and usage-report retries.

package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/runner"
)

type fakeRunner struct {
	result ai.DecisionResult
	err    error
}

type runnerFunc func(context.Context, ai.DecisionSession) (ai.DecisionResult, error)

func (f runnerFunc) Run(ctx context.Context, session ai.DecisionSession) (ai.DecisionResult, error) {
	return f(ctx, session)
}

type agentRoundTrip func(*http.Request) (*http.Response, error)

func (f agentRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type usageReportingTransport struct {
	cancel context.CancelFunc
	claims *atomic.Int32
	usage  *atomic.Int32
}

func (transport usageReportingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Header.Get("Authorization") != "Bearer secret" {
		return agentResponse(http.StatusUnauthorized, ""), nil
	}
	switch request.URL.Path {
	case "/internal/agent/claim":
		return transport.claimResponse(request), nil
	case "/internal/agent/usage":
		transport.usage.Add(1)
		transport.cancel()
	}
	return agentResponse(http.StatusNoContent, ""), nil
}

func (transport usageReportingTransport) claimResponse(request *http.Request) *http.Response {
	var claimRequest struct {
		ProfileIDs  []string `json:"profileIds"`
		WaitSeconds int      `json:"waitSeconds"`
	}
	if json.NewDecoder(request.Body).Decode(&claimRequest) != nil || len(claimRequest.ProfileIDs) != 1 || claimRequest.ProfileIDs[0] != "profile" || claimRequest.WaitSeconds < 1 {
		return agentResponse(http.StatusBadRequest, "")
	}
	if transport.claims.Add(1) != 1 {
		return agentResponse(http.StatusNoContent, "")
	}
	encoded, _ := json.Marshal(job{RequestID: "request", ProfileID: "profile"})
	return agentResponse(http.StatusOK, string(encoded))
}

func agentResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func (r fakeRunner) Run(context.Context, ai.DecisionSession) (ai.DecisionResult, error) {
	return r.result, r.err
}

func TestWorkerClaimsRunsAndReportsUsage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var claims atomic.Int32
	var usage atomic.Int32
	client := &http.Client{Transport: usageReportingTransport{cancel: cancel, claims: &claims, usage: &usage}}
	profiles := ai.NewProfileRegistry("cluster", []ai.ModelProfile{{ID: "profile", DisplayName: "Profile", Provider: "openai", Runtime: "api", Scope: "cluster", Enabled: true, MaxSteps: 1, MaxOutputTokens: 1}})
	worker := &Worker{MCPURL: "http://mcp/mcp", Secret: "secret", WorkerID: "agent", Profiles: profiles, PollInterval: time.Millisecond, DecisionTimeout: time.Second, Concurrency: 2, Client: client, RunnerFactory: func(ai.ModelProfile) (ai.DecisionRunner, error) {
		return fakeRunner{result: ai.DecisionResult{Accepted: true, Usage: ai.Usage{Requests: 1}}}, nil
	}}
	if err := worker.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if usage.Load() != 1 {
		t.Fatalf("usage=%d", usage.Load())
	}
}

func TestWorkerValidationAndDefaultProvider(t *testing.T) {
	worker := &Worker{Concurrency: 1}
	if err := worker.Run(context.Background()); err == nil {
		t.Fatal("low concurrency accepted")
	}
	if _, err := defaultRunner(ai.ModelProfile{Runtime: "api", Provider: "unknown"}); err == nil {
		t.Fatal("unknown provider accepted")
	}
	for _, profile := range []ai.ModelProfile{{Runtime: "cli", Provider: "codex-cli"}, {Runtime: "api", Provider: "openai"}, {Runtime: "api", Provider: "gemini"}} {
		if decisionRunner, err := defaultRunner(profile); err != nil || decisionRunner == nil {
			t.Fatalf("profile=%#v err=%v", profile, err)
		}
	}
	decisionRunner, err := defaultRunner(ai.ModelProfile{Runtime: "cli", Provider: "codex-cli", Model: "gpt-5.6-luna"})
	cliRunner, ok := decisionRunner.(runner.CLIRunner)
	if err != nil || !ok || cliRunner.Model != "gpt-5.6-luna" {
		t.Fatalf("CLI runner=%#v err=%v", decisionRunner, err)
	}
}

func TestRunnerOutcomeClassifiesDeadlineAndCompletion(t *testing.T) {
	deadlineContext, cancelDeadline := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelDeadline()
	canceledContext, cancel := context.WithCancel(context.Background())
	cancel()
	for _, test := range []struct {
		name     string
		ctx      context.Context
		err      error
		accepted bool
		want     string
	}{
		{name: "deadline", ctx: deadlineContext, err: errors.New("killed"), want: "deadline_exceeded"},
		{name: "canceled", ctx: canceledContext, err: errors.New("killed"), want: "canceled"},
		{name: "execution", ctx: context.Background(), err: errors.New("failed"), want: "execution_error"},
		{name: "not accepted", ctx: context.Background(), want: "not_accepted"},
		{name: "completed", ctx: context.Background(), accepted: true, want: "completed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := runnerOutcome(test.ctx, test.err, test.accepted); got != test.want {
				t.Fatalf("runnerOutcome()=%q want %q", got, test.want)
			}
		})
	}
}

func TestWorkerDoesNotClaimBeyondAvailableConcurrency(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var claims atomic.Int32
	client := &http.Client{Transport: agentRoundTrip(func(request *http.Request) (*http.Response, error) {
		if strings.HasSuffix(request.URL.Path, "/claim") {
			id := claims.Add(1)
			body, _ := json.Marshal(job{RequestID: fmt.Sprintf("request-%d", id), ProfileID: "profile"})
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(body))), Header: make(http.Header)}, nil
		}
		return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})}
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	profiles := ai.NewProfileRegistry("cluster", []ai.ModelProfile{{ID: "profile", DisplayName: "Profile", Provider: "openai", Runtime: "api", Scope: "cluster", Enabled: true, MaxSteps: 1, MaxOutputTokens: 1}})
	worker := &Worker{MCPURL: "http://mcp/mcp", Secret: "secret", WorkerID: "agent", Profiles: profiles, DecisionTimeout: time.Second, Concurrency: 2, Client: client, RunnerFactory: func(ai.ModelProfile) (ai.DecisionRunner, error) {
		return runnerFunc(func(context.Context, ai.DecisionSession) (ai.DecisionResult, error) {
			started <- struct{}{}
			<-release
			return ai.DecisionResult{Accepted: true}, nil
		}), nil
	}}
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	<-started
	<-started
	time.Sleep(10 * time.Millisecond)
	if claims.Load() != 2 {
		t.Fatalf("claimed %d jobs for two execution slots", claims.Load())
	}
	cancel()
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestWorkerClaimAndReportingFailures(t *testing.T) {
	clientFor := func(status int, body string, transportErr error) *http.Client {
		return &http.Client{Transport: agentRoundTrip(func(*http.Request) (*http.Response, error) {
			if transportErr != nil {
				return nil, transportErr
			}
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
		})}
	}
	worker := &Worker{MCPURL: "http://mcp/mcp", Secret: "secret", WorkerID: "agent", DecisionTimeout: time.Second}
	worker.Client = clientFor(http.StatusInternalServerError, "", nil)
	if _, _, err := worker.claim(context.Background(), []string{"profile"}); err == nil {
		t.Fatal("claim status accepted")
	}
	worker.Client = clientFor(http.StatusOK, "not-json", nil)
	if _, _, err := worker.claim(context.Background(), []string{"profile"}); err == nil {
		t.Fatal("bad claim accepted")
	}
	want := errors.New("network")
	worker.Client = clientFor(0, "", want)
	if _, _, err := worker.claim(context.Background(), []string{"profile"}); !errors.Is(err, want) {
		t.Fatal(err)
	}
	worker.Client = clientFor(http.StatusInternalServerError, "", nil)
	if err := worker.fail(context.Background(), "request", "provider", ai.DecisionResult{}); err == nil {
		t.Fatal("fail status accepted")
	}
	if err := worker.reportUsage(context.Background(), "request", ai.DecisionResult{}); err == nil {
		t.Fatal("usage status accepted")
	}
}

func TestRunJobReportsUnavailableAndRunnerFailures(t *testing.T) {
	var failures atomic.Int32
	var reportedOutcome string
	var logs bytes.Buffer
	client := &http.Client{Transport: agentRoundTrip(func(request *http.Request) (*http.Response, error) {
		if strings.HasSuffix(request.URL.Path, "/fail") {
			failures.Add(1)
			var payload struct {
				Metadata map[string]any `json:"metadata"`
			}
			_ = json.NewDecoder(request.Body).Decode(&payload)
			if outcome, ok := payload.Metadata["runnerOutcome"].(string); ok {
				reportedOutcome = outcome
			}
		}
		return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})}
	profiles := ai.NewProfileRegistry("cluster", []ai.ModelProfile{{ID: "profile", DisplayName: "Profile", Provider: "openai", Runtime: "api", Scope: "cluster", Enabled: true, MaxSteps: 1, MaxOutputTokens: 1}})
	worker := &Worker{MCPURL: "http://mcp/mcp", Secret: "secret", Profiles: profiles, DecisionTimeout: time.Second, Client: client, Logger: slog.New(slog.NewJSONHandler(&logs, nil)), RunnerFactory: func(ai.ModelProfile) (ai.DecisionRunner, error) { return fakeRunner{err: errors.New("provider")}, nil }}
	worker.runJob(context.Background(), job{RequestID: "request", ProfileID: "missing"})
	worker.runJob(context.Background(), job{RequestID: "request", ProfileID: "profile"})
	worker.RunnerFactory = func(ai.ModelProfile) (ai.DecisionRunner, error) { return nil, errors.New("factory") }
	worker.runJob(context.Background(), job{RequestID: "request", ProfileID: "profile"})
	if failures.Load() != 3 {
		t.Fatalf("failures=%d", failures.Load())
	}
	if reportedOutcome != "execution_error" || !strings.Contains(logs.String(), `"msg":"decision_runner_failed"`) || !strings.Contains(logs.String(), `"runner_outcome":"execution_error"`) {
		t.Fatalf("outcome=%q logs=%s", reportedOutcome, logs.String())
	}
}

func TestCompletionWithoutPlanFailsImmediately(t *testing.T) {
	var failed bool
	var logs bytes.Buffer
	w := &Worker{MCPURL: "http://mcp/mcp", Client: &http.Client{Transport: agentRoundTrip(func(r *http.Request) (*http.Response, error) {
		status := http.StatusConflict
		if strings.HasSuffix(r.URL.Path, "/fail") {
			var body struct {
				Category string
				Usage    ai.Usage
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Category != "plan_not_submitted" || body.Usage.Requests != 1 {
				t.Fatal("failure lost reason or usage")
			}
			failed = true
			status = http.StatusNoContent
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})}}
	ctx := context.Background()
	w.finishJob(ctx, ctx, jobRun{logger: slog.New(slog.NewJSONHandler(&logs, nil)), started: time.Now(), requestID: "request"}, ai.DecisionResult{Accepted: true, Usage: ai.Usage{Requests: 1}}, nil)
	if !failed || strings.Contains(logs.String(), "decision_runner_completed") {
		t.Fatal("false success was not converted to failure")
	}
}

func TestUsageRetriesTransientEOF(t *testing.T) {
	attempts := 0
	w := &Worker{MCPURL: "http://mcp/mcp", Client: &http.Client{Transport: agentRoundTrip(func(r *http.Request) (*http.Response, error) {
		attempts++
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal("retry lost request body")
		}
		if attempts == 1 {
			return nil, io.EOF
		}
		return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}}
	if err := w.reportUsage(context.Background(), "request", ai.DecisionResult{}); err != nil || attempts != 2 {
		t.Fatalf("retry failed %d %v", attempts, err)
	}
}
