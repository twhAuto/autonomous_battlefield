// Checks bounded database readiness, process health routes, and separation from business-
// route middleware.

package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestReadinessReturnsOKWhenDatabaseResponds(t *testing.T) {
	router := NewLifecycleHandler()
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
	router := NewLifecycleHandler()
	router.Activate(http.NotFoundHandler(), pingerFunc(func(context.Context) error {
		return errors.New("database is unavailable")
	}), nil)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	if body := response.Body.String(); body != "database unavailable\n" {
		t.Fatalf("body = %q, want %q", body, "database unavailable\\n")
	}
}

func TestReadinessBoundsDatabasePing(t *testing.T) {
	router := NewLifecycleHandler()
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
	router := NewLifecycleHandler()
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
