// Checks AI gateway startup reports a failed database connection and readiness follows
// database availability.

package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/config"
)

func TestRunRejectsInvalidDatabaseConnection(t *testing.T) {
	configuration := config.AIGatewayConfig{Database: config.Database{URL: "://bad", ConnectTimeout: time.Millisecond, MaxConnections: 1}, Gateway: config.Gateway{CapabilitySecret: "secret", Address: ":8084"}}
	if err := run(context.Background(), configuration); err == nil {
		t.Fatal("invalid database accepted")
	}
}

type pingerFunc func(context.Context) error

func (f pingerFunc) Ping(ctx context.Context) error { return f(ctx) }

func TestReadinessFollowsDatabaseAvailability(t *testing.T) {
	cases := []struct {
		name string
		ping pingerFunc
		want int
	}{
		{"database reachable", func(context.Context) error { return nil }, http.StatusNoContent},
		{"database unavailable", func(context.Context) error { return errors.New("down") }, http.StatusServiceUnavailable},
		{"ping exceeds timeout", func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }, http.StatusServiceUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			readinessHandler(tc.ping, 10*time.Millisecond)(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			if recorder.Code != tc.want {
				t.Fatalf("status = %d, want %d", recorder.Code, tc.want)
			}
		})
	}
}

func TestRunReportsTracingConfigurationErrors(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1")
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "2")
	if err := run(context.Background(), config.AIGatewayConfig{}); err == nil || !strings.Contains(err.Error(), "configure tracing") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunServesUntilCancelled(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	configuration := config.AIGatewayConfig{
		Database: config.Database{URL: url, MaxConnections: 2, ConnectTimeout: 5 * time.Second},
		Gateway:  config.Gateway{CapabilitySecret: strings.Repeat("c", 32), AgentSecret: strings.Repeat("a", 32), Address: "127.0.0.1:0"},
	}
	if err := run(ctx, configuration); err != nil {
		t.Fatalf("gateway stopped with %v, want a clean shutdown", err)
	}
}
