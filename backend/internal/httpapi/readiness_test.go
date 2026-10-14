// Checks bounded database readiness, process health routes, and separation from business-
// route middleware.

package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/apierror"
)

func TestReadinessReturnsOKWhenDatabaseResponds(t *testing.T) {
	router := NewLifecycleHandler(nil)
	router.Activate(http.NotFoundHandler(), pingerFunc(func(context.Context) error {
		return nil
	}), nil)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if body := response.Body.String(); body != "ok\n" {
		t.Fatalf("body = %q, want %q", body, "ok\\n")
	}
}

func TestReadinessReturnsUnavailableWhenDatabasePingFails(t *testing.T) {
	router := NewLifecycleHandler(nil)
	router.Activate(http.NotFoundHandler(), pingerFunc(func(context.Context) error {
		return errors.New("database is unavailable")
	}), nil)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	var envelope struct {
		Error apierror.ErrorBody `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("response is not a JSON error envelope: %v (%q)", err, response.Body.String())
	}
	if envelope.Error.Code != apierror.CodeUnavailable || envelope.Error.Message != "database unavailable" {
		t.Fatalf("error = %+v, want service_unavailable / database unavailable", envelope.Error)
	}
	// Kubernetes probes read only the status; the underlying ping error must not leak.
	if strings.Contains(response.Body.String(), "database is unavailable") {
		t.Fatalf("response leaked the underlying ping error: %q", response.Body.String())
	}
}

func TestReadinessBoundsDatabasePing(t *testing.T) {
	router := NewLifecycleHandler(nil)
	router.Activate(http.NotFoundHandler(), pingerFunc(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}), nil)
	router.timeout = time.Millisecond
	response := httptest.NewRecorder()

	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}

func TestLifecycleHealthEndpoints(t *testing.T) {
	router := NewLifecycleHandler(nil)
	router.Activate(http.NotFoundHandler(), pingerFunc(func(context.Context) error { return nil }), nil)
	for _, path := range []string{"/healthz", "/livez", "/startupz"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d", path, response.Code)
		}
	}
}

func TestRouterAppliesMiddlewareInsideRequestLogging(t *testing.T) {
	called := false
	middleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			called = true
			next.ServeHTTP(response, request)
		})
	}
	router := NewRouterWithMiddleware([]Middleware{middleware})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if !called || response.Header().Get("X-Request-ID") == "" {
		t.Fatalf("middleware called=%t request ID=%q", called, response.Header().Get("X-Request-ID"))
	}
}

type pingerFunc func(context.Context) error

func (f pingerFunc) Ping(ctx context.Context) error {
	return f(ctx)
}

func TestApplicationRouterDoesNotOwnProcessProbes(t *testing.T) {
	router := NewRouter()
	for _, path := range []string{"/healthz", "/livez", "/startupz", "/readyz"} {
		assertStatus(t, router, path, http.StatusNotFound)
	}
}
