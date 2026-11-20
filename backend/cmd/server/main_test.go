// Checks API and migration command options and managed Player startup.

package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/authn"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/config"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/members"
)

func TestMigrateCommandReportsAnUnreachableDatabase(t *testing.T) {
	t.Setenv("BATTLEFIELD_ENV", "")
	t.Setenv("BATTLEFIELD_AUTH_MODE", "")
	t.Setenv("BATTLEFIELD_AUTH_ENABLED", "false")
	t.Setenv("DATABASE_URL", "postgres://battlefield@127.0.0.1:1/battlefield?sslmode=disable")
	if code := command().Main([]string{"-migrate", "-config", "../../config.yaml"}); code != 1 {
		t.Fatalf("migration against a closed port exited %d, want 1", code)
	}
	if code := command().Main([]string{"-migrate", "up"}); code != 1 {
		t.Fatalf("positional argument exited %d, want 1", code)
	}
}

func TestStartServerStopsWhenItsListenerFails(t *testing.T) {
	err := startServer(context.Background(), apiConfig("bad address"))
	if err == nil || !strings.Contains(err.Error(), "serve:") {
		t.Fatalf("error = %v, want the listener failure", err)
	}
}

func TestStartServerStopsCleanlyOnSignalBeforeTheDatabaseIsReady(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := startServer(ctx, apiConfig("127.0.0.1:0")); err != nil {
		t.Fatalf("shutdown while waiting for the database returned %v", err)
	}
}

// apiConfig serves probes on address and points at a closed database port.
func apiConfig(address string) config.APIConfig {
	return config.APIConfig{
		HTTP:     config.HTTPServer{Address: address, ReadHeaderTimeout: time.Second, ShutdownTimeout: time.Second},
		Database: config.Database{URL: "postgres://battlefield@127.0.0.1:1/battlefield?sslmode=disable", MaxConnections: 1, ConnectTimeout: 10 * time.Millisecond},
	}
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
	if repository := membersRepository(&members.Store{}); repository == nil {
		t.Fatal("an enabled store became a nil repository")
	}
}

func TestManagedPlayersRejectInvalidConfiguredPlayer(t *testing.T) {
	_, _, err := startManagedPlayers(context.Background(), nil, config.Members{Enabled: true}, map[string]authn.Role{"user@localhost": authn.Player})
	if err == nil {
		t.Fatal("invalid configured Player accepted")
	}
}

func TestStartServerReportsTracingConfigurationErrors(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1")
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "2")
	if err := startServer(context.Background(), apiConfig("127.0.0.1:0")); err == nil || !strings.Contains(err.Error(), "configure tracing") {
		t.Fatalf("error = %v", err)
	}
}

func TestMigrateCommandAppliesPendingMigrations(t *testing.T) {
	url := testDatabaseURL(t)
	t.Setenv("BATTLEFIELD_ENV", "")
	t.Setenv("BATTLEFIELD_AUTH_MODE", "")
	t.Setenv("BATTLEFIELD_AUTH_ENABLED", "false")
	t.Setenv("DATABASE_URL", url)
	if code := command().Main([]string{"-migrate", "-config", "../../config.yaml"}); code != 0 {
		t.Fatalf("migration exited %d, want 0", code)
	}
}

func TestStartServerStopsWhenInitializationFails(t *testing.T) {
	url := testDatabaseURL(t)
	for name, change := range map[string]func(*config.APIConfig){
		"result cache": func(c *config.APIConfig) { c.ResultCache = config.ResultCache{URL: "redis://127.0.0.1:1"} },
		"managed Players": func(c *config.APIConfig) {
			c.Members = config.Members{Enabled: true}
			c.Auth.Roles = map[string]authn.Role{"user@localhost": authn.Player}
		},
	} {
		t.Run(name, func(t *testing.T) {
			configuration := apiConfig("127.0.0.1:0")
			configuration.Database.URL = url
			configuration.Database.ConnectTimeout = 5 * time.Second
			change(&configuration)
			if err := startServer(context.Background(), configuration); err == nil || !strings.Contains(err.Error(), "initialize:") {
				t.Fatalf("error = %v, want an initialization failure", err)
			}
		})
	}
}

func testDatabaseURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	return url
}
