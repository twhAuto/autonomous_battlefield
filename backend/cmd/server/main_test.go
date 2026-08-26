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

func TestParseOptionsDefaultsToStartingServer(t *testing.T) {
	options, err := parseOptions(nil)
	if err != nil {
		t.Fatalf("parseOptions() returned an error: %v", err)
	}

	if options.configPath != "config.yaml" {
		t.Fatalf("configPath = %q, want config.yaml", options.configPath)
	}
	if options.migrate {
		t.Fatal("migrate = true, want false")
	}
}

func TestParseOptionsAcceptsMigrationMode(t *testing.T) {
	options, err := parseOptions([]string{"-migrate", "-config", "testdata/config.yaml"})
	if err != nil {
		t.Fatalf("parseOptions() returned an error: %v", err)
	}

	if options.configPath != "testdata/config.yaml" {
		t.Fatalf("configPath = %q, want testdata/config.yaml", options.configPath)
	}
	if !options.migrate {
		t.Fatal("migrate = false, want true")
	}
}

func TestParseOptionsRejectsPositionalArguments(t *testing.T) {
	if _, err := parseOptions([]string{"up"}); err == nil {
		t.Fatal("parseOptions() returned no error")
	}
}

func TestStopAPIInitialization(t *testing.T) {
	initializationError := errors.New("initialization failed")
	for _, test := range []struct {
		name         string
		signal       context.Context
		serverError  error
		wantNil      bool
		wantContains string
	}{
		{name: "initialization error", signal: context.Background(), wantContains: "initialize API"},
		{name: "signal cancellation", signal: cancelledContext(), wantNil: true},
		{name: "server error", signal: context.Background(), serverError: errors.New("serve failed"), wantContains: "serve API"},
	} {
		t.Run(test.name, func(t *testing.T) {
			serverErrors := make(chan error, 1)
			serverErrors <- test.serverError
			err := stopAPIInitialization(
				&http.Server{},
				httpapi.NewLifecycleHandler(http.NotFoundHandler()),
				serverErrors,
				test.signal,
				time.Second,
				initializationError,
			)
			if test.wantNil && err != nil {
				t.Fatalf("stopAPIInitialization() error = %v, want nil", err)
			}
			if !test.wantNil && (err == nil || !strings.Contains(err.Error(), test.wantContains)) {
				t.Fatalf("stopAPIInitialization() error = %v, want text %q", err, test.wantContains)
			}
		})
	}
}

func cancelledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}
