package worker

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPolicyDisablesRunningExecution(t *testing.T) {
	w := &Worker{MCPURL: "http://private/mcp", Secret: "worker-only", PolicyPolling: true, Client: &http.Client{Transport: agentRoundTrip(func(r *http.Request) (*http.Response, error) {
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
	w := &Worker{PolicyPolling: true}
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
