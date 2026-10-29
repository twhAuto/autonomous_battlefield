// Checks model job claiming, concurrency limits, runner selection, cancellation outcomes,
// and usage-report retries.

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/profile"
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
	encoded, _ := json.Marshal(job{CapabilityToken: "server-issued-capability", RequestID: "request", ProfileID: "profile"})
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
	profiles := profile.NewRegistry("cluster", []ai.ModelProfile{{ID: "profile", DisplayName: "Profile", Provider: "openai", Runtime: "api", Scope: "cluster", Enabled: true, MaxSteps: 1, MaxOutputTokens: 1}})
	worker := &Executor{MCPURL: "http://mcp/mcp", Secret: "secret", WorkerID: "agent", Profiles: profiles, PollInterval: time.Millisecond, DecisionTimeout: time.Second, Concurrency: 2, Client: client, RunnerFactory: func(ai.ModelProfile) (ai.DecisionRunner, error) {
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
	worker := &Executor{Concurrency: 1}
	if err := worker.Run(context.Background()); err == nil {
		t.Fatal("low concurrency accepted")
	}
	if _, err := defaultRunner(ai.ModelProfile{Runtime: "api", Provider: "unknown"}); err == nil {
		t.Fatal("unknown provider accepted")
	}
	for _, profile := range []ai.ModelProfile{{Runtime: "cli", Provider: "claude-cli"}, {Runtime: "cli", Provider: "codex-cli"}, {Runtime: "api", Provider: "openai"}, {Runtime: "api", Provider: "gemini"}} {
		if decisionRunner, err := defaultRunner(profile); err != nil || decisionRunner == nil {
			t.Fatalf("profile=%#v err=%v", profile, err)
		}
	}
	decisionRunner, err := defaultRunner(ai.ModelProfile{Runtime: "cli", Provider: "codex-cli", Model: "gpt-5.6-luna"})
	cliRunner, ok := decisionRunner.(runner.CLIRunner)
	if err != nil || !ok || cliRunner.Model != "gpt-5.6-luna" || !cliRunner.PrefetchContext {
		t.Fatalf("CLI runner=%#v err=%v", decisionRunner, err)
	}
	optOut := false
	decisionRunner, _ = defaultRunner(ai.ModelProfile{Runtime: "cli", Provider: "codex-cli", PrefetchContext: &optOut})
	if decisionRunner.(runner.CLIRunner).PrefetchContext {
		t.Fatal("explicit prefetchContext: false still prefetched")
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
			body, _ := json.Marshal(job{CapabilityToken: "server-issued-capability", RequestID: fmt.Sprintf("request-%d", id), ProfileID: "profile"})
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(body))), Header: make(http.Header)}, nil
		}
		return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})}
	started := make(chan struct{}, 4)
	release := make(chan struct{})
	defer close(release)

	profiles := profile.NewRegistry("cluster", []ai.ModelProfile{{ID: "profile", DisplayName: "Profile", Provider: "openai", Runtime: "api", Scope: "cluster", Enabled: true, MaxSteps: 1, MaxOutputTokens: 1}})
	worker := &Executor{MCPURL: "http://mcp/mcp", Secret: "secret", WorkerID: "agent", Profiles: profiles, DecisionTimeout: time.Second, Concurrency: 4, Client: client, RunnerFactory: func(ai.ModelProfile) (ai.DecisionRunner, error) {
		return runnerFunc(func(ctx context.Context, _ ai.DecisionSession) (ai.DecisionResult, error) {
			started <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
			}
			return ai.DecisionResult{Accepted: true}, nil
		}), nil
	}}
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	for range 4 {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("four runners did not start concurrently")
		}
	}
	time.Sleep(10 * time.Millisecond)
	if claims.Load() != 4 {
		t.Fatalf("claimed %d jobs for four execution slots", claims.Load())
	}
	cancel()
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
	worker := &Executor{MCPURL: "http://mcp/mcp", Secret: "secret", WorkerID: "agent", DecisionTimeout: time.Second}
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
	profiles := profile.NewRegistry("cluster", []ai.ModelProfile{{ID: "profile", DisplayName: "Profile", Provider: "openai", Runtime: "api", Scope: "cluster", Enabled: true, MaxSteps: 1, MaxOutputTokens: 1}})
	worker := &Executor{MCPURL: "http://mcp/mcp", Secret: "secret", Profiles: profiles, DecisionTimeout: time.Second, Client: client, Logger: slog.New(slog.NewJSONHandler(&logs, nil)), RunnerFactory: func(ai.ModelProfile) (ai.DecisionRunner, error) {
		return fakeRunner{err: errors.New("provider said 429\nwith Bearer secret-value")}, nil
	}}
	worker.runJob(context.Background(), job{CapabilityToken: "server-issued-capability", RequestID: "request", ProfileID: "missing"})
	worker.runJob(context.Background(), job{CapabilityToken: "server-issued-capability", RequestID: "request", ProfileID: "profile"})
	worker.RunnerFactory = func(ai.ModelProfile) (ai.DecisionRunner, error) { return nil, errors.New("factory") }
	worker.runJob(context.Background(), job{CapabilityToken: "server-issued-capability", RequestID: "request", ProfileID: "profile"})
	if failures.Load() != 3 {
		t.Fatalf("failures=%d", failures.Load())
	}
	if reportedOutcome != "execution_error" || !strings.Contains(logs.String(), `"msg":"decision_runner_failed"`) || !strings.Contains(logs.String(), `"runner_outcome":"execution_error"`) {
		t.Fatalf("outcome=%q logs=%s", reportedOutcome, logs.String())
	}
	if !strings.Contains(logs.String(), `"error":"provider said 429 with Bearer [redacted]"`) || strings.Contains(logs.String(), "secret-value") {
		t.Fatalf("runner error detail missing or unredacted: %s", logs.String())
	}
}

func TestRunnerFailureLogRedactsNetworkIdentity(t *testing.T) {
	var logs bytes.Buffer
	refused := &net.OpError{Op: "dial", Net: "tcp", Addr: &net.TCPAddr{IP: net.ParseIP("10.0.0.5"), Port: 18084}, Err: errors.New("connect: connection refused")}
	lookup := &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "no such host", Name: "battlefield-ai-gateway.internal"}}
	run := jobRun{logger: slog.New(slog.NewJSONHandler(&logs, nil)), started: time.Now()}
	run.logFailure(context.Background(), "mcp_context", "execution_error", fmt.Errorf("prefetch: %w", errors.Join(refused, lookup)))
	if strings.Contains(logs.String(), "10.0.0.5") || strings.Contains(logs.String(), "battlefield-ai-gateway.internal") ||
		!strings.Contains(logs.String(), "dial tcp: connect: connection refused") || !strings.Contains(logs.String(), "dial tcp: lookup failed: no such host") {
		t.Fatalf("network identity leaked or cause lost: %s", logs.String())
	}
}

func TestCompletionWithoutPlanFailsImmediately(t *testing.T) {
	var failed bool
	var logs bytes.Buffer
	w := &Executor{MCPURL: "http://mcp/mcp", Client: &http.Client{Transport: agentRoundTrip(func(r *http.Request) (*http.Response, error) {
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
	w := &Executor{MCPURL: "http://mcp/mcp", Client: &http.Client{Transport: agentRoundTrip(func(r *http.Request) (*http.Response, error) {
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

func TestTransientClaimFailureDoesNotCancelSibling(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan context.Context, 1)
	retried := make(chan struct{})
	var claims atomic.Int32
	client := &http.Client{Transport: agentRoundTrip(func(req *http.Request) (*http.Response, error) {
		switch claims.Add(1) {
		case 1:
			b, _ := json.Marshal(job{CapabilityToken: "capability", RequestID: "one", ProfileID: "profile"})
			return agentResponse(200, string(b)), nil
		case 2:
			return agentResponse(503, ""), nil
		default:
			select {
			case <-retried:
			default:
				close(retried)
			}
			<-req.Context().Done()
			return nil, req.Context().Err()
		}
	})}
	profiles := profile.NewRegistry("cluster", []ai.ModelProfile{{ID: "profile", DisplayName: "Profile", Provider: "openai", Runtime: "api", Scope: "cluster", Enabled: true, MaxSteps: 1, MaxOutputTokens: 1}})
	w := &Executor{MCPURL: "http://mcp/mcp", Profiles: profiles, Concurrency: 2, Client: client, PollInterval: time.Millisecond, DecisionTimeout: time.Minute, RunnerFactory: func(ai.ModelProfile) (ai.DecisionRunner, error) {
		return runnerFunc(func(ctx context.Context, _ ai.DecisionSession) (ai.DecisionResult, error) {
			started <- ctx
			<-ctx.Done()
			return ai.DecisionResult{}, ctx.Err()
		}), nil
	}}
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	var running context.Context
	select {
	case running = <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not start")
	}
	select {
	case <-retried:
	case <-time.After(2 * time.Second):
		t.Fatal("claim not retried")
	}
	if running.Err() != nil {
		t.Fatal("transient claim cancelled sibling")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker failed to stop")
	}
}

func TestServiceURLDerivesControlAndGenerationEndpoints(t *testing.T) {
	for _, mcpURL := range []string{"http://mcp:8084/mcp", "http://mcp:8084/mcp/", "http://mcp:8084", "http://mcp:8084/"} {
		w := &Executor{MCPURL: mcpURL}
		if got := w.serviceURL("/internal/agent/claim"); got != "http://mcp:8084/internal/agent/claim" {
			t.Errorf("serviceURL(%q) = %q", mcpURL, got)
		}
		if got := w.serviceURL("/generation-mcp"); got != "http://mcp:8084/generation-mcp" {
			t.Errorf("generation URL for %q = %q", mcpURL, got)
		}
	}
}

// signallingRunner reports each start on started, then waits for finish. Both waits
// honor cancellation: after Resume, finished jobs are re-claimed until the test cancels,
// and a blocking send on a full started channel would never see that cancellation.
func signallingRunner(started chan<- struct{}, finish <-chan struct{}) ai.DecisionRunner {
	return runnerFunc(func(ctx context.Context, _ ai.DecisionSession) (ai.DecisionResult, error) {
		select {
		case started <- struct{}{}:
		case <-ctx.Done():
			return ai.DecisionResult{}, ctx.Err()
		}
		select {
		case <-finish:
			return ai.DecisionResult{Accepted: true}, nil
		case <-ctx.Done():
			return ai.DecisionResult{}, ctx.Err()
		}
	})
}

func TestDrainStopsNewClaimsAndLetsRunningDecisionsFinish(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var claims atomic.Int32
	client := &http.Client{Transport: agentRoundTrip(func(request *http.Request) (*http.Response, error) {
		if strings.HasSuffix(request.URL.Path, "/claim") {
			id := claims.Add(1)
			body, _ := json.Marshal(job{CapabilityToken: "server-issued-capability", RequestID: fmt.Sprintf("request-%d", id), ProfileID: "profile"})
			return agentResponse(http.StatusOK, string(body)), nil
		}
		return agentResponse(http.StatusNoContent, ""), nil
	})}
	started := make(chan struct{}, 4)
	finish := make(chan struct{})
	profiles := profile.NewRegistry("cluster", []ai.ModelProfile{{ID: "profile", DisplayName: "Profile", Provider: "openai", Runtime: "api", Scope: "cluster", Enabled: true, MaxSteps: 1, MaxOutputTokens: 1}})
	worker := &Executor{MCPURL: "http://mcp/mcp", Secret: "secret", WorkerID: "agent", Profiles: profiles, PollInterval: time.Millisecond, DecisionTimeout: time.Second, Concurrency: 1, Client: client, RunnerFactory: func(ai.ModelProfile) (ai.DecisionRunner, error) {
		return signallingRunner(started, finish), nil
	}}
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	<-started
	worker.Drain()
	if status := worker.Status(); !status.Draining || status.Active != 1 || status.Concurrency != 1 {
		t.Fatalf("status while draining a running decision = %+v", status)
	}
	close(finish)
	deadline := time.Now().Add(2 * time.Second)
	for worker.Status().Active != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the running decision did not finish after drain")
		}
		time.Sleep(time.Millisecond)
	}
	claimed := claims.Load()
	time.Sleep(20 * time.Millisecond)
	if claims.Load() != claimed {
		t.Fatal("a draining worker claimed new work")
	}
	worker.Resume()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("a resumed worker did not claim again")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestWorkerExecutesFrozenSelectionOrFailsUnavailable(t *testing.T) {
	t.Setenv("SELECTION_CLI_AVAILABLE", "true")
	profiles := profile.NewCatalog("cluster", []ai.ModelProfile{{ID: "cli", DisplayName: "CLI", Provider: "codex-cli", Runtime: "cli", Scope: "local", Enabled: true, Model: "fast", SupportedVersionPrefix: "codex-cli 0.155.", AvailabilityEnvironmentVariable: "SELECTION_CLI_AVAILABLE", ModelOptions: []ai.ModelOption{{ID: "deep", ReasoningEfforts: []string{"low"}}}}})
	for _, test := range []struct {
		model      string
		invoked    bool
		reportPath string
	}{{"deep", true, "/internal/agent/usage"}, {"removed", false, "/internal/agent/fail"}} {
		invoked := false
		reportPath := ""
		worker := &Executor{Profiles: profiles, DecisionTimeout: time.Second, MCPURL: "http://mcp/mcp", Client: &http.Client{Transport: agentRoundTrip(func(req *http.Request) (*http.Response, error) {
			reportPath = req.URL.Path
			return agentResponse(204, ""), nil
		})}, RunnerFactory: func(p ai.ModelProfile) (ai.DecisionRunner, error) {
			invoked = true
			if p.Model != "deep" || p.ReasoningEffort != "low" {
				t.Fatalf("wrong selection: %+v", p)
			}
			return runnerFunc(func(_ context.Context, s ai.DecisionSession) (ai.DecisionResult, error) {
				if s.Profile.Model != "deep" {
					t.Fatal("wrong session model")
				}
				return ai.DecisionResult{Accepted: true}, nil
			}), nil
		}}
		claimed := time.Now()
		worker.runJob(context.Background(), job{RequestID: "request", ProfileID: "cli", CapabilityToken: "scoped", ModelIdentifier: test.model, ReasoningEffort: "low", CreatedAt: claimed.Add(-time.Second), ClaimedAt: &claimed})
		if test.invoked != invoked {
			t.Fatal("unexpected execution", test.model, invoked)
		}
		if reportPath != test.reportPath {
			t.Fatal(reportPath)
		}
	}
}

func TestAPIWorkerUsesCurrentConfiguredModelAfterEnvironmentChange(t *testing.T) {
	t.Setenv("API_SELECTION_MODEL", "current-model")
	profiles := profile.NewRegistry("cluster", []ai.ModelProfile{{ID: "api", DisplayName: "API", Provider: "openai", Runtime: "api", Scope: "cluster", Enabled: true, ModelEnvironmentVariable: "API_SELECTION_MODEL"}})
	invoked, reportPath := false, ""
	worker := &Executor{Profiles: profiles, DecisionTimeout: time.Second, MCPURL: "http://mcp/mcp", Client: &http.Client{Transport: agentRoundTrip(func(req *http.Request) (*http.Response, error) {
		reportPath = req.URL.Path
		return agentResponse(204, ""), nil
	})}, RunnerFactory: func(p ai.ModelProfile) (ai.DecisionRunner, error) {
		invoked = true
		if p.ModelLabel != "current-model" || p.ReasoningEffort != "" {
			t.Fatalf("API model configuration changed: %+v", p)
		}
		return fakeRunner{result: ai.DecisionResult{Accepted: true}}, nil
	}}
	worker.runJob(context.Background(), job{RequestID: "request", ProfileID: "api", CapabilityToken: "scoped", ModelIdentifier: "previous-model"})
	if !invoked || reportPath != "/internal/agent/usage" {
		t.Fatalf("invoked = %t, report = %q", invoked, reportPath)
	}
}

func TestStatusReportsServedAndUnavailableProfiles(t *testing.T) {
	profiles := profile.NewRegistry("cluster", []ai.ModelProfile{
		{ID: "profile", DisplayName: "Profile", Provider: "openai", Runtime: "api", Scope: "cluster", Enabled: true, MaxSteps: 1, MaxOutputTokens: 1},
		{ID: "off", DisplayName: "Off", Provider: "openai", Runtime: "api", Scope: "cluster", MaxSteps: 1, MaxOutputTokens: 1},
	})
	status := (&Executor{Profiles: profiles, Concurrency: 2}).Status()
	if !reflect.DeepEqual(status.Profiles, []string{"profile"}) || len(status.Unavailable) != 1 || status.Unavailable["off"] != "disabled by configuration" {
		t.Fatalf("status = %+v", status)
	}
	if empty := (&Executor{Concurrency: 1}).Status(); empty.Profiles == nil || len(empty.Profiles) != 0 || empty.Unavailable != nil {
		t.Fatalf("status without a registry = %+v", empty)
	}
}
