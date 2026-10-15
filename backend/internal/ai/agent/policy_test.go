package agent

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPolicyDisablesRunningExecution(t *testing.T) {
	w := &Executor{MCPURL: "http://private/mcp", Secret: "worker-only", PolicyPolling: true, Client: &http.Client{Transport: agentRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/internal/agent/enabled" || r.Header.Get("Authorization") != "Bearer worker-only" {
			t.Error("incorrect policy authentication")
		}
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}}
	ctx, cancel := w.watchPolicy(context.Background())
	defer cancel()
	select {
	case <-ctx.Done():
	case <-time.After(4 * time.Second):
		t.Fatal("operator stop did not cancel worker")
	}
}

func TestDelayedClaimCannotExtendExecutionLease(t *testing.T) {
	w := &Executor{PolicyPolling: true}
	for _, deadline := range []time.Time{time.Time{}, time.Now().Add(-time.Second)} {
		ctx, cancel := w.executionContext(context.Background(), job{ExecutionDeadline: deadline}, 2*time.Minute)
		if ctx.Err() == nil {
			t.Fatal("missing or expired lease allowed execution")
		}
		cancel()
	}
	expiry := time.Now().Add(time.Second)
	ctx, cancel := w.executionContext(context.Background(), job{ExecutionDeadline: expiry}, time.Minute)
	defer cancel()
	got, _ := ctx.Deadline()
	if !got.Equal(expiry) {
		t.Fatal("claim arrival extended reservation")
	}
}

func TestPolicyCheckFailsClosed(t *testing.T) {
	enabled := agentRoundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	unreachable := agentRoundTrip(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("connection refused")
	})
	for name, w := range map[string]*Executor{
		"malformed gateway URL": {MCPURL: "http://bad host/mcp", Client: &http.Client{Transport: enabled}},
		"gateway unreachable":   {MCPURL: "http://private/mcp", Client: &http.Client{Transport: unreachable}},
	} {
		if w.policyEnabled(context.Background()) {
			t.Fatalf("%s: policy check reported enabled", name)
		}
	}
	if !(&Executor{MCPURL: "http://private/mcp", Client: &http.Client{Transport: enabled}}).policyEnabled(context.Background()) {
		t.Fatal("204 policy response was not treated as enabled")
	}
}
