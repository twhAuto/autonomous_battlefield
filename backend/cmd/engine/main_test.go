// Checks the engine stops cleanly when its probe listener fails or a signal arrives
// before the database is ready.

package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/config"
)

func TestStartEngineStopsWhenItsListenerFails(t *testing.T) {
	err := startEngine(context.Background(), engineConfig("bad address"))
	if err == nil || !strings.Contains(err.Error(), "serve:") {
		t.Fatalf("error = %v, want the listener failure", err)
	}
}

func TestStartEngineStopsCleanlyOnSignalBeforeTheDatabaseIsReady(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := startEngine(ctx, engineConfig("127.0.0.1:0")); err != nil {
		t.Fatalf("shutdown while waiting for the database returned %v", err)
	}
}

// engineConfig serves probes on address and points at a closed database port.
func engineConfig(address string) config.EngineConfig {
	return config.EngineConfig{
		Engine:   config.Engine{HealthAddress: address, ShutdownTimeout: time.Second},
		Database: config.Database{URL: "postgres://battlefield@127.0.0.1:1/battlefield?sslmode=disable", MaxConnections: 1, ConnectTimeout: 10 * time.Millisecond},
	}
}

func TestStartEngineReportsTracingConfigurationErrors(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1")
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "2")
	if err := startEngine(context.Background(), engineConfig("127.0.0.1:0")); err == nil || !strings.Contains(err.Error(), "configure tracing") {
		t.Fatalf("error = %v", err)
	}
}

func TestStartEngineStopsWhenTheWorkerCannotBeBuilt(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	configuration := engineConfig("127.0.0.1:0")
	configuration.Database.URL = url
	configuration.Database.ConnectTimeout = 5 * time.Second
	// A zero poll interval is not a valid worker configuration.
	if err := startEngine(context.Background(), configuration); err == nil || !strings.Contains(err.Error(), "initialize:") {
		t.Fatalf("error = %v, want the worker initialization failure", err)
	}
}
