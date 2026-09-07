// Checks engine flag parsing, startup cleanup, and worker identity selection.

package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/httpapi"
)

func TestParseOptions(t *testing.T) {
	options, err := parseOptions([]string{"-config", "engine.yaml"})
	if err != nil {
		t.Fatalf("parseOptions() returned an error: %v", err)
	}
	if options.configPath != "engine.yaml" {
		t.Fatalf("configPath = %q, want engine.yaml", options.configPath)
	}
}

func TestParseOptionsRejectsPositionals(t *testing.T) {
	if _, err := parseOptions([]string{"run"}); err == nil {
		t.Fatal("parseOptions() returned no error")
	}
}

func TestStopEngineInitialization(t *testing.T) {
	initializationError := errors.New("initialization failed")
	for _, test := range []struct {
		name         string
		signal       context.Context
		serverError  error
		wantNil      bool
		wantContains string
	}{
		{name: "initialization error", signal: context.Background(), wantContains: "initialize engine"},
		{name: "signal cancellation", signal: cancelledEngineContext(), wantNil: true},
		{name: "server error", signal: context.Background(), serverError: errors.New("serve failed"), wantContains: "serve engine health"},
	} {
		t.Run(test.name, func(t *testing.T) {
			healthErrors := make(chan error, 1)
			healthErrors <- test.serverError
			err := stopEngineInitialization(
				&http.Server{},
				httpapi.NewLifecycleHandler(http.NotFoundHandler()),
				healthErrors,
				test.signal,
				time.Second,
				initializationError,
			)
			if test.wantNil && err != nil {
				t.Fatalf("stopEngineInitialization() error = %v, want nil", err)
			}
			if !test.wantNil && (err == nil || !strings.Contains(err.Error(), test.wantContains)) {
				t.Fatalf("stopEngineInitialization() error = %v, want text %q", err, test.wantContains)
			}
		})
	}
}

func TestWorkerIDUsesConfiguredEnvironmentOrHost(t *testing.T) {
	t.Setenv("TEST_ENGINE_WORKER_ID", "configured-worker")
	if id := workerID("TEST_ENGINE_WORKER_ID"); id != "configured-worker" {
		t.Fatalf("workerID() = %q, want configured-worker", id)
	}
	t.Setenv("TEST_ENGINE_WORKER_ID", "")
	if id := workerID("TEST_ENGINE_WORKER_ID"); !strings.Contains(id, "-") {
		t.Fatalf("workerID() fallback = %q, want hostname-pid", id)
	}
}

func cancelledEngineContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}
