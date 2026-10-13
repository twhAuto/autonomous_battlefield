// Checks API composition, authenticated route access, cached results, and shared request
// trace context.

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"

	"github.com/twh09/autonomous_battlefield/backend/internal/apierror"
	"github.com/twh09/autonomous_battlefield/backend/internal/logging"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	"github.com/twh09/autonomous_battlefield/backend/internal/battle/catalog"
	"github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/httpapi"
)

func TestNewAPIComposesHTTPApplication(t *testing.T) {
	application := New(appDatabase{}, &catalog.Catalog{}, 10_000, Options{})
	response := httptest.NewRecorder()
	lifecycle := httpapi.NewLifecycleHandler()
	lifecycle.Activate(application.HTTPHandler(), appDatabase{}, nil)
	lifecycle.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("health status = %d, want %d", response.Code, http.StatusOK)
	}
}

func TestNewAPIWithAuthenticationProtectsApplicationRoutes(t *testing.T) {
	application := New(appDatabase{}, &catalog.Catalog{}, 10_000, Options{Auth: authn.Config{
		Enabled: true, Username: "admin", Password: "a-long-test-password",
		SessionSecret: "a-test-session-secret-with-32-characters", SessionTTL: time.Hour,
	}})
	response := httptest.NewRecorder()
	application.HTTPHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/factions", nil))
	if response.Code != http.StatusUnauthorized || response.Header().Get("X-Request-ID") == "" {
		t.Fatalf("status=%d request ID=%q", response.Code, response.Header().Get("X-Request-ID"))
	}
	body := assembledError(t, response)
	if body.Code != apierror.CodeAuthenticationRequired || body.RequestID != response.Header().Get("X-Request-ID") {
		t.Fatalf("error = %+v, want authentication_required with the X-Request-ID header value", body)
	}
}

// Every unmatched /v1 request must still produce the envelope. Unregistered routes are
// denied by RoutePolicy rather than reported missing, so their existence is not
// revealed; the auth routes bypass that policy, so a wrong method on them reaches
// ServeMux, whose plain-text 405 the router replaces.
func TestAssembledAPIReturnsTheEnvelopeForUnmatchedRequests(t *testing.T) {
	handler := New(appDatabase{}, &catalog.Catalog{}, 10_000, Options{}).HTTPHandler()

	for _, denied := range [][2]string{{http.MethodGet, "/v1/no-such-route"}, {http.MethodPatch, "/v1/factions"}, {http.MethodGet, "/v1/auth/no-such-route"}} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(denied[0], denied[1], nil))
		if body := assembledError(t, response); response.Code != http.StatusForbidden || body.Code != apierror.CodePermissionDenied || body.RequestID == "" {
			t.Fatalf("%s %s = %d %+v, want default-deny 403 permission_denied with a request ID", denied[0], denied[1], response.Code, body)
		}
	}

	for _, wrong := range []struct{ method, path, allow string }{
		{http.MethodGet, "/v1/auth/login", "POST"},
		{http.MethodDelete, "/v1/auth/session", "GET, HEAD"},
		{http.MethodPut, "/v1/auth/logout", "POST"},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(wrong.method, wrong.path, nil))
		if body := assembledError(t, response); response.Code != http.StatusMethodNotAllowed || body.Code != apierror.CodeMethodNotAllowed {
			t.Fatalf("%s %s = %d %+v, want 405 method_not_allowed", wrong.method, wrong.path, response.Code, body)
		}
		if allow := response.Header().Get("Allow"); allow != wrong.allow {
			t.Fatalf("%s %s Allow = %q, want %q", wrong.method, wrong.path, allow, wrong.allow)
		}
	}
}

func assembledError(t *testing.T, response *httptest.ResponseRecorder) apierror.ErrorBody {
	t.Helper()
	var envelope struct {
		Error apierror.ErrorBody `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("response is not a JSON error envelope: %v (%q)", err, response.Body.String())
	}
	return envelope.Error
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
	application := New(appDatabase{}, &catalog.Catalog{}, 100, Options{Cache: cache})
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
	protected := New(appDatabase{}, &catalog.Catalog{}, 100, Options{Auth: authn.Config{Enabled: true, SessionSecret: "test-secret-with-at-least-32-characters", SessionTTL: time.Hour}, Cache: cache})
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
	application := New(appDatabase{}, &catalog.Catalog{}, 10_000, Options{Auth: authn.Config{Enabled: true}})
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
