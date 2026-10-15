// Checks agent startup rejects invalid addresses and that shutdown waits for in-flight
// jobs to report before the process exits.

package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/config"
)

func TestRunRejectsInvalidListenOrMCPAddress(t *testing.T) {
	configuration := config.AgentConfig{Models: config.Models{Environment: "cluster", DecisionTimeout: time.Millisecond}, Agent: config.Agent{Concurrency: 1, HealthAddress: "bad address", MCPURL: "://bad"}}
	if err := run(context.Background(), configuration); err == nil {
		t.Fatal("invalid runtime addresses accepted")
	}
	if hostname() == "" {
		t.Fatal("hostname empty")
	}
}

// slowRunner stands in for agent.Executor: after cancellation it keeps working for
// finishDelay, as jobs do while reporting their outcomes.
type slowRunner struct {
	finishDelay time.Duration
	err         error
	finished    atomic.Bool
}

func (r *slowRunner) Run(ctx context.Context) error {
	<-ctx.Done()
	time.Sleep(r.finishDelay)
	r.finished.Store(true)
	return r.err
}

func healthServer() *http.Server {
	return &http.Server{Addr: "127.0.0.1:0", ReadHeaderTimeout: time.Second}
}

func TestShutdownWaitsForInFlightJobs(t *testing.T) {
	for _, runErr := range []error{nil, context.Canceled} {
		ctx, cancel := context.WithCancel(context.Background())
		runner := &slowRunner{finishDelay: 50 * time.Millisecond, err: runErr}
		go func() { time.Sleep(20 * time.Millisecond); cancel() }()
		if err := serveUntilStopped(ctx, healthServer(), runner, time.Second); err != nil {
			t.Fatalf("shutdown with executor result %v returned %v", runErr, err)
		}
		if !runner.finished.Load() {
			t.Fatal("the process stopped before in-flight jobs finished reporting")
		}
	}
}

func TestShutdownGivesUpOnJobsThatDoNotFinish(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := serveUntilStopped(ctx, healthServer(), &slowRunner{finishDelay: time.Second}, 20*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "agent jobs did not finish") {
		t.Fatalf("error = %v, want the shutdown deadline", err)
	}
}

func TestExecutorAndHealthServerFailuresStopTheProcess(t *testing.T) {
	failure := errors.New("claim loop failed")
	if err := serveUntilStopped(context.Background(), healthServer(), failingRunner{failure}, time.Second); !errors.Is(err, failure) {
		t.Fatalf("executor failure = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := serveUntilStopped(ctx, &http.Server{Addr: "bad address"}, &slowRunner{}, time.Second); err == nil {
		t.Fatal("health server failure ignored")
	}
}

type failingRunner struct{ err error }

func (r failingRunner) Run(context.Context) error { return r.err }

func TestCheckAuthNamesTheCLIAndBoundsTheCheck(t *testing.T) {
	var deadline time.Time
	ok := func(ctx context.Context) error { deadline, _ = ctx.Deadline(); return nil }
	if err := checkAuth(context.Background(), "Claude", ok); err != nil || time.Until(deadline) > authCheckTimeout {
		t.Fatalf("passing check: err=%v deadline in %v", err, time.Until(deadline))
	}
	missing := errors.New("not logged in")
	err := checkAuth(context.Background(), "Antigravity", func(context.Context) error { return missing })
	if !errors.Is(err, missing) || !strings.Contains(err.Error(), "Antigravity auth unavailable") {
		t.Fatalf("failing check: %v", err)
	}
}

func TestCommandRunsOnlyTheRequestedAuthCheck(t *testing.T) {
	var ran []string
	check := func(cli string, err error) func(context.Context) error {
		return func(context.Context) error { ran = append(ran, cli); return err }
	}
	for _, test := range []struct {
		flag    string
		failing string
		want    int
	}{
		{"-check-claude-auth", "", 0},
		{"-check-antigravity-auth", "", 0},
		{"-check-claude-auth", "claude", 1},
	} {
		ran = nil
		claudeErr, antigravityErr := error(nil), error(nil)
		if test.failing == "claude" {
			claudeErr = errors.New("logged out")
		}
		// No -config file exists: an auth check must finish without loading one.
		code := command(check("claude", claudeErr), check("antigravity", antigravityErr)).Main([]string{test.flag, "-config", "missing.yaml"})
		if code != test.want || len(ran) != 1 || !strings.Contains(test.flag, ran[0]) {
			t.Fatalf("%s: code=%d ran=%v", test.flag, code, ran)
		}
	}
	if code := command(check("claude", nil), check("antigravity", nil)).Main([]string{"-config", "missing.yaml"}); code != 1 {
		t.Fatalf("missing configuration exited %d, want 1", code)
	}
}

func TestRunReportsTracingConfigurationErrors(t *testing.T) {
	invalidTracing(t)
	if err := run(context.Background(), config.AgentConfig{}); err == nil || !strings.Contains(err.Error(), "configure tracing") {
		t.Fatalf("error = %v", err)
	}
}

// invalidTracing enables trace export with an out-of-range sampler ratio.
func invalidTracing(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1")
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "2")
}
