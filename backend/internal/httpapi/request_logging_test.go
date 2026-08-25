package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/telemetry"
)

func TestRequestLoggingRecordsResponseAndCorrelationID(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	handler := requestLogging(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if RequestID(r.Context()) != "request-123" {
			t.Fatalf("request ID in context = %q", RequestID(r.Context()))
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("ok"))
	}), logger)

	request := httptest.NewRequest(http.MethodPost, "/v1/battles", nil)
	request.Header.Set("X-Request-ID", "request-123")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Header().Get("X-Request-ID") != "request-123" {
		t.Fatalf("response request ID = %q", response.Header().Get("X-Request-ID"))
	}
	var entry map[string]any
	if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
		t.Fatalf("decode log entry: %v", err)
	}
	if entry["msg"] != "http_request_completed" || entry["status"] != float64(http.StatusCreated) || entry["request_id"] != "request-123" {
		t.Fatalf("log entry = %#v", entry)
	}
}

func TestRequestLoggingGeneratesRequestID(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	handler := requestLogging(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), logger)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/factions", nil))
	if response.Header().Get("X-Request-ID") == "" {
		t.Fatal("response request ID is empty")
	}
}

func TestRequestMetricsUseNormalizedRoutePattern(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/battles/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := requestLogging(mux, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))
	handler.ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/v1/battles/battle-specific-id", nil),
	)

	response := httptest.NewRecorder()
	telemetry.MetricsHandler().ServeHTTP(
		response,
		httptest.NewRequest(http.MethodGet, "/metrics", nil),
	)
	metric := `battlefield_http_server_requests_total{code="200",method="GET",route="GET /v1/battles/{id}"}`
	if !strings.Contains(response.Body.String(), metric) {
		t.Fatalf("metrics do not contain normalized route %q", metric)
	}
}
