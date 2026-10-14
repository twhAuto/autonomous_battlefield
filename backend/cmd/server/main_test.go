// Checks API and migration command options and cleanup of interrupted initialization.

package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	"github.com/twh09/autonomous_battlefield/backend/internal/config"
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

func TestManagedPlayersDisabledStartsNothing(t *testing.T) {
	store, stop, err := startManagedPlayers(context.Background(), nil, config.Members{}, nil)
	if err != nil || store != nil || stop == nil {
		t.Fatalf("store=%v stop=%v err=%v", store, stop != nil, err)
	}
	stop()
	if repository := membersRepository(nil); repository != nil {
		t.Fatal("a disabled store became a non-nil repository")
	}
}

func TestManagedPlayersRejectInvalidConfiguredPlayer(t *testing.T) {
	_, _, err := startManagedPlayers(context.Background(), nil, config.Members{Enabled: true}, map[string]authn.Role{"user@localhost": authn.Player})
	if err == nil {
		t.Fatal("invalid configured Player accepted")
	}
}
