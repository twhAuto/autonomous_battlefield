// Checks AI gateway startup reports a failed database connection and readiness follows
// database availability.

package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/config"
)

func TestRunRejectsInvalidDatabaseConnection(t *testing.T) {
	configuration := config.Config{Database: config.Database{URL: "://bad", ConnectTimeout: time.Millisecond, MaxConnections: 1}, AI: config.AI{CapabilitySecret: "secret", AIGatewayAddress: ":8084"}}
	if err := run(configuration); err == nil {
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
