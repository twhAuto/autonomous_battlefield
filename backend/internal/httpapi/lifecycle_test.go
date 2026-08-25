package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLifecycleHandlerTransitions(t *testing.T) {
	lifecycle := NewLifecycleHandler()

	assertStatus(t, lifecycle, "/livez", http.StatusOK)
	assertStatus(t, lifecycle, "/startupz", http.StatusServiceUnavailable)
	assertStatus(t, lifecycle, "/readyz", http.StatusServiceUnavailable)
	assertStatus(t, lifecycle, "/v1/factions", http.StatusServiceUnavailable)

	application := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	lifecycle.Activate(application, pingerFunc(func(context.Context) error { return nil }), nil)

	assertStatus(t, lifecycle, "/startupz", http.StatusOK)
	assertStatus(t, lifecycle, "/readyz", http.StatusOK)
	assertStatus(t, lifecycle, "/v1/factions", http.StatusNoContent)

	lifecycle.BeginShutdown()
	assertStatus(t, lifecycle, "/livez", http.StatusOK)
	assertStatus(t, lifecycle, "/startupz", http.StatusOK)
	assertStatus(t, lifecycle, "/readyz", http.StatusServiceUnavailable)
	assertStatus(t, lifecycle, "/v1/factions", http.StatusServiceUnavailable)
}

func TestLifecycleReadinessChecksWorkerAndDatabase(t *testing.T) {
	lifecycle := NewLifecycleHandler()
	workerReady := false
	databaseError := error(nil)
	lifecycle.Activate(
		http.NotFoundHandler(),
		pingerFunc(func(context.Context) error { return databaseError }),
		func() bool { return workerReady },
	)

	assertStatus(t, lifecycle, "/readyz", http.StatusServiceUnavailable)
	workerReady = true
	assertStatus(t, lifecycle, "/readyz", http.StatusOK)
	databaseError = errors.New("database unavailable")
	assertStatus(t, lifecycle, "/readyz", http.StatusServiceUnavailable)
}

func TestLifecycleMetricsRemainAvailableOutsideApplicationLifecycle(t *testing.T) {
	metrics := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("test_metric 1\n"))
	})
	lifecycle := NewLifecycleHandler(metrics)

	assertStatus(t, lifecycle, "/metrics", http.StatusOK)
	lifecycle.BeginShutdown()
	assertStatus(t, lifecycle, "/metrics", http.StatusOK)
}

func assertStatus(t *testing.T, handler http.Handler, path string, expected int) {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	if response.Code != expected {
		t.Fatalf("GET %s status = %d, want %d", path, response.Code, expected)
	}
}
