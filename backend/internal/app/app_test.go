// Checks API composition, authenticated route access, cached results, and shared request
// trace context.

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/twh09/autonomous_battlefield/backend/internal/logging"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	"github.com/twh09/autonomous_battlefield/backend/internal/battle/catalog"
	"github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/httpapi"
)

func TestNewAPIComposesHTTPApplication(t *testing.T) {
	application := NewAPI(appDatabase{}, &catalog.Catalog{}, 10_000)
	response := httptest.NewRecorder()
	lifecycle := httpapi.NewLifecycleHandler()
	lifecycle.Activate(application.HTTPHandler(), appDatabase{}, nil)
	lifecycle.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("health status = %d, want %d", response.Code, http.StatusOK)
	}
}

func TestNewAPIWithAuthenticationProtectsApplicationRoutes(t *testing.T) {
	application := NewAPIWithAuthentication(appDatabase{}, &catalog.Catalog{}, 10_000, authn.Config{
		Enabled: true, Username: "admin", Password: "a-long-test-password",
		SessionSecret: "a-test-session-secret-with-32-characters", SessionTTL: time.Hour,
	})
	response := httptest.NewRecorder()
	application.HTTPHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/factions", nil))
	if response.Code != http.StatusUnauthorized || response.Header().Get("X-Request-ID") == "" {
		t.Fatalf("status=%d request ID=%q", response.Code, response.Header().Get("X-Request-ID"))
	}
}

type appDatabase struct{}

func (appDatabase) Ping(context.Context) error { return nil }
func (appDatabase) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (appDatabase) Query(context.Context, string, ...any) (pgx.Rows, error) { return nil, nil }
func (appDatabase) QueryRow(context.Context, string, ...any) pgx.Row        { return nil }

type cachedBattle struct{ reads int }

func (c *cachedBattle) Get(_ context.Context, id string) (*service.BattleRun, error) {
	c.reads++
	return &service.BattleRun{ID: id, Status: "completed", CurrentTick: 42, Result: &game.Result{}}, nil
}
func (c *cachedBattle) Set(context.Context, *service.BattleRun) error { return nil }

func TestCachedResultHTTPAndAuthentication(t *testing.T) {
	const id = "abcdefab-1234-1234-1234-123456789012"
	cache := &cachedBattle{}
	// appDatabase.QueryRow returns nil: this request must be served entirely from cache.
	application := NewAPIWithResultCache(appDatabase{}, &catalog.Catalog{}, 100, authn.Config{}, cache)
	response := httptest.NewRecorder()
	application.HTTPHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/battles/"+id, nil))
	var result struct {
		ID          string
		Status      string
		CurrentTick uint64
		Result      *game.Result
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || result.ID != id || result.Status != "completed" || result.CurrentTick != 42 || result.Result == nil || cache.reads != 1 {
		t.Fatalf("cached HTTP response: %s", response.Body.String())
	}
	protected := NewAPIWithResultCache(appDatabase{}, &catalog.Catalog{}, 100, authn.Config{Enabled: true, SessionSecret: "test-secret-with-at-least-32-characters", SessionTTL: time.Hour}, cache)
	response = httptest.NewRecorder()
	protected.HTTPHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/battles/"+id, nil))
	if response.Code != http.StatusUnauthorized || cache.reads != 1 {
		t.Fatal("authentication did not precede cache lookup")
	}
}

func TestAccessLogSharesIncomingTrace(t *testing.T) {
	var output bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(logging.NewHandler(slog.NewJSONHandler(&output, nil))))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })
	previousProvider := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()
	provider := tracesdk.NewTracerProvider()
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		otel.SetTracerProvider(previousProvider)
		otel.SetTextMapPropagator(previousPropagator)
		_ = provider.Shutdown(context.Background())
	})
	application := NewAPIWithAuthentication(appDatabase{}, &catalog.Catalog{}, 10_000, authn.Config{Enabled: true})
	request := httptest.NewRequest(http.MethodGet, "/v1/factions", nil)
	request.Header.Set("traceparent", "00-01000000000000000000000000000000-0200000000000000-01")
	request.Header.Set("X-Request-ID", "request-123")
	response := httptest.NewRecorder()
	application.HTTPHandler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatal(response.Code)
	}
	var row map[string]any
	if err := json.Unmarshal(output.Bytes(), &row); err != nil {
		t.Fatal(err, output.String())
	}
	if row["event"] != "http_request_completed" || row["trace_id"] != "01000000000000000000000000000000" || row["span_id"] == nil || row["request_id"] != "request-123" || row["duration_ms"] == nil {
		t.Fatal(row)
	}
}
