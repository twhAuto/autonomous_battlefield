package worker

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// watchPolicy cancels in-flight inference when the operator disables AI. Transport
// failure also cancels: a disconnected worker cannot continue spending indefinitely.
func (w *Worker) watchPolicy(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	if !w.PolicyPolling {
		return ctx, cancel
	}
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			check, done := context.WithTimeout(ctx, 2*time.Second)
			req, err := http.NewRequestWithContext(check, "GET", strings.TrimSuffix(w.MCPURL, "/mcp")+"/internal/agent/enabled", nil)
			if err != nil {
				done()
				cancel()
				return
			}
			req.Header.Set("Authorization", bearerPrefix+w.Secret)
			response, err := w.Client.Do(req)
			enabled := err == nil && response.StatusCode == http.StatusNoContent
			if response != nil {
				response.Body.Close()
			}
			done()
			if !enabled {
				cancel()
				return
			}
		}
	}()
	return ctx, cancel
}

// Use the database lease deadline, not response-arrival time: a delayed claim
// response must never extend execution beyond the slot reservation.
func (w *Worker) executionContext(parent context.Context, job job, timeout time.Duration) (context.Context, context.CancelFunc) {
	deadline := time.Now().Add(timeout)
	if !job.ExecutionDeadline.IsZero() && job.ExecutionDeadline.Before(deadline) {
		deadline = job.ExecutionDeadline
	}
	if w.PolicyPolling && job.ExecutionDeadline.IsZero() {
		deadline = time.Now()
	}
	return context.WithDeadline(parent, deadline)
}
