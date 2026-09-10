// Checks application metrics are recorded and exposed through the Prometheus handler.

package telemetry

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestApplicationMetricsAreRecordedAndExposed(t *testing.T) {
	ObserveHTTPRequest(http.MethodGet, "", http.StatusNotFound, 2*time.Millisecond)
	ObserveHTTPRequest(http.MethodPost, "POST /v1/battles", http.StatusAccepted, 3*time.Millisecond)
	ObserveHTTPRequest(http.MethodGet, "GET /v1/battles", http.StatusInternalServerError, time.Millisecond)
	ObserveEngineClaim(false)
	ObserveEngineClaim(true)
	ObserveEngineBattle("completed", time.Second)
	ObserveEngineTick()
	SetEngineReady(true)
	SetEngineReady(false)

	response := httptest.NewRecorder()
	MetricsHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want %d", response.Code, http.StatusOK)
	}
	for _, metric := range []string{
		"battlefield_http_server_requests_total",
		`route="unmatched"`,
		"battlefield_engine_battle_claims_total",
		"battlefield_engine_battles_processed_total",
		"battlefield_engine_ticks_total",
		"battlefield_engine_worker_ready 0",
		`battlefield_http_server_request_duration_seconds_count{method="GET",outcome="error",route="GET /v1/battles"} 1`,
		`battlefield_http_server_request_duration_seconds_count{method="POST",outcome="success",route="POST /v1/battles"} 1`,
	} {
		if !strings.Contains(response.Body.String(), metric) {
			t.Fatalf("metrics output does not contain %q", metric)
		}
	}
}
