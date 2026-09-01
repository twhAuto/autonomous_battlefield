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
	router := NewRouter(NewReadinessRoutes(pingerFunc(func(context.Context) error {
		return nil
	})))
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
	router := NewRouter(NewReadinessRoutes(pingerFunc(func(context.Context) error {
		return errors.New("database is unavailable")
	})))
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
	routes := NewReadinessRoutes(pingerFunc(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}))
	routes.timeout = time.Millisecond
	router := NewRouter(routes)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}

func TestHealthEndpoints(t *testing.T) {
	router := NewRouter()
	for _, path := range []string{"/healthz", "/livez", "/startupz"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d", path, response.Code)
		}
	}
}

type pingerFunc func(context.Context) error

func (f pingerFunc) Ping(ctx context.Context) error {
	return f(ctx)
}
