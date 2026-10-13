package agent

import (
	"context"
	"net/http"
	"time"
)

// watchPolicy cancels in-flight inference when the operator disables AI. Transport
// failure also cancels: a disconnected worker cannot continue spending indefinitely.
func (e *Executor) watchPolicy(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	if !e.PolicyPolling {
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
			if !e.policyEnabled(ctx) {
				cancel()
				return
			}
		}
	}()
	return ctx, cancel
}

// policyEnabled reports whether the control plane currently allows model work.
// Any request or transport failure counts as disabled.
func (e *Executor) policyEnabled(ctx context.Context) bool {
	check, done := context.WithTimeout(ctx, 2*time.Second)
	defer done()
	req, err := http.NewRequestWithContext(check, http.MethodGet, e.serviceURL("/internal/agent/enabled"), nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", bearerPrefix+e.Secret)
	response, err := e.Client.Do(req)
	if err != nil {
		return false
	}
	response.Body.Close()
	return response.StatusCode == http.StatusNoContent
}

// Use the database lease deadline, not response-arrival time: a delayed claim
// response must never extend execution beyond the slot reservation.
func (e *Executor) executionContext(parent context.Context, job job, timeout time.Duration) (context.Context, context.CancelFunc) {
	deadline := time.Now().Add(timeout)
	if !job.ExecutionDeadline.IsZero() && job.ExecutionDeadline.Before(deadline) {
		deadline = job.ExecutionDeadline
	}
	if e.PolicyPolling && job.ExecutionDeadline.IsZero() {
		deadline = time.Now()
	}
	return context.WithDeadline(parent, deadline)
}
