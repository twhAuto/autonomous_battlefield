package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/apierror"
)

// The request ID the logging middleware assigns must reach the error envelope, so a
// client-reported failure can be matched to the server log line for the same request.
func TestErrorEnvelopeCarriesTheMiddlewareRequestID(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := requestLogging(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apierror.Write(w, r, http.StatusInternalServerError, "could not load battle")
	}), logger)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/battles/1", nil)
	request.Header.Set("X-Request-ID", "req-abc123")
	handler.ServeHTTP(recorder, request)

	var envelope struct {
		Error apierror.ErrorBody `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("response is not a JSON error envelope: %v", err)
	}
	if envelope.Error.RequestID != "req-abc123" {
		t.Fatalf("requestId = %q, want req-abc123", envelope.Error.RequestID)
	}
	if header := recorder.Header().Get("X-Request-ID"); header != envelope.Error.RequestID {
		t.Fatalf("X-Request-ID header %q does not match envelope requestId %q", header, envelope.Error.RequestID)
	}
}
