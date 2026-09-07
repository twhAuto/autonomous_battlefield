// Checks request correlation IDs, response logging, and bounded route labels for HTTP
// metrics.

package httpapi

import (
	"bytes"
	"context"
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

func TestRequestLoggingHandlesNilLoggerAndStatusLevels(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusInternalServerError, http.StatusOK} {
		handler := requestLogging(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			w.WriteHeader(http.StatusTeapot)
		}), nil)
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil))
	}
}

func TestLoggingResponseWriterAndRequestIDHelpers(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer := &loggingResponseWriter{ResponseWriter: recorder, status: http.StatusOK}
	if writer.Unwrap() != recorder {
		t.Fatal("Unwrap() did not return the underlying writer")
	}
	if _, err := writer.Write([]byte("ok")); err != nil || writer.status != http.StatusOK || writer.bytes != 2 {
		t.Fatalf("writer = %#v, error = %v", writer, err)
	}
	if acceptedRequestID(strings.Repeat("x", 129)) == strings.Repeat("x", 129) {
		t.Fatal("acceptedRequestID() accepted an oversized ID")
	}
	if stringID(0) != "0" || stringID(12345) != "12345" {
		t.Fatalf("stringID() results = %q, %q", stringID(0), stringID(12345))
	}
	if RequestID(context.Background()) != "" {
		t.Fatal("RequestID() returned a value for an empty context")
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
