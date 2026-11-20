package apierror_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/apierror"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/observability/requestid"
)

func decode(t *testing.T, body string) apierror.ErrorBody {
	t.Helper()
	var envelope struct {
		Error apierror.ErrorBody `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("response is not a JSON error envelope: %v (body %q)", err, body)
	}
	return envelope.Error
}

func TestWriteUsesJSONEnvelopeWithStatusDefaultCode(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/battles", nil)

	apierror.Write(recorder, request, http.StatusNotFound, "battle not found")

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", contentType)
	}
	if sniff := recorder.Header().Get("X-Content-Type-Options"); sniff != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", sniff)
	}
	body := decode(t, recorder.Body.String())
	if body.Code != apierror.CodeNotFound || body.Message != "battle not found" {
		t.Fatalf("body = %+v, want not_found / battle not found", body)
	}
}

func TestWriteIncludesTheRequestIDInScope(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/battles/1", nil)
	request = request.WithContext(requestid.With(request.Context(), "req-abc123"))

	apierror.Write(recorder, request, http.StatusInternalServerError, "could not load battle")

	body := decode(t, recorder.Body.String())
	if body.RequestID != "req-abc123" {
		t.Fatalf("requestId = %q, want req-abc123", body.RequestID)
	}
	if body.Code != apierror.CodeInternal {
		t.Fatalf("code = %q, want %q", body.Code, apierror.CodeInternal)
	}
}

func TestWriteOmitsTheRequestIDWhenNoneIsInScope(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/scenarios", nil)

	apierror.Write(recorder, request, http.StatusBadRequest, "invalid query")

	if strings.Contains(recorder.Body.String(), "requestId") {
		t.Fatalf("expected requestId to be omitted, got %q", recorder.Body.String())
	}
	if code := decode(t, recorder.Body.String()).Code; code != apierror.CodeInvalidRequest {
		t.Fatalf("code = %q, want %q", code, apierror.CodeInvalidRequest)
	}
}

func TestWriteCodeOverridesTheStatusDefault(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/battles", nil)

	// Battle creation distinguishes a duplicate identifier from other conflicts.
	apierror.WriteCode(recorder, request, http.StatusConflict, apierror.CodeAlreadyExists, "battle already exists")

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusConflict)
	}
	if code := decode(t, recorder.Body.String()).Code; code != apierror.CodeAlreadyExists {
		t.Fatalf("code = %q, want %q", code, apierror.CodeAlreadyExists)
	}
}

func TestWriteFallsBackToStatusTextForAnEmptyMessage(t *testing.T) {
	recorder := httptest.NewRecorder()

	apierror.Write(recorder, httptest.NewRequest(http.MethodGet, "/v1/battles", nil), http.StatusForbidden, "")

	body := decode(t, recorder.Body.String())
	if body.Message != http.StatusText(http.StatusForbidden) || body.Code != apierror.CodePermissionDenied {
		t.Fatalf("body = %+v, want permission_denied / %q", body, http.StatusText(http.StatusForbidden))
	}
}

func TestWriteToleratesANilRequest(t *testing.T) {
	recorder := httptest.NewRecorder()

	apierror.Write(recorder, nil, http.StatusServiceUnavailable, "database unavailable")

	body := decode(t, recorder.Body.String())
	if body.RequestID != "" || body.Code != apierror.CodeUnavailable {
		t.Fatalf("body = %+v, want service_unavailable without requestId", body)
	}
}

func TestDefaultCodeCoversMappedAndUnmappedStatuses(t *testing.T) {
	for status, want := range map[int]apierror.ErrorCode{
		http.StatusBadRequest:           apierror.CodeInvalidRequest,
		http.StatusUnauthorized:         apierror.CodeAuthenticationRequired,
		http.StatusForbidden:            apierror.CodePermissionDenied,
		http.StatusNotFound:             apierror.CodeNotFound,
		http.StatusMethodNotAllowed:     apierror.CodeMethodNotAllowed,
		http.StatusConflict:             apierror.CodeConflict,
		http.StatusUnsupportedMediaType: apierror.CodeUnsupportedMedia,
		http.StatusUnprocessableEntity:  apierror.CodeValidationFailed,
		http.StatusTooManyRequests:      apierror.CodeRateLimited,
		http.StatusInternalServerError:  apierror.CodeInternal,
		http.StatusNotImplemented:       apierror.CodeNotImplemented,
		http.StatusServiceUnavailable:   apierror.CodeUnavailable,
		// Unmapped statuses still yield a usable code by class.
		http.StatusTeapot:         apierror.CodeInvalidRequest,
		http.StatusBadGateway:     apierror.CodeInternal,
		http.StatusGatewayTimeout: apierror.CodeInternal,
	} {
		if got := apierror.DefaultCode(status); got != want {
			t.Errorf("DefaultCode(%d) = %q, want %q", status, got, want)
		}
	}
}
