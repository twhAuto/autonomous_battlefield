// Defines the stable JSON error envelope shared by every public API handler so clients
// can branch on a documented code instead of parsing prose. It is a leaf package so
// that authn, which httpapi imports, can use it without an import cycle.

package apierror

import (
	"encoding/json"
	"net/http"

	"github.com/twh09/autonomous_battlefield/backend/internal/observability/requestid"
)

// ErrorCode is a stable, machine-readable identifier for a failure class. Values are
// part of the public API contract: add new codes rather than renaming existing ones.
type ErrorCode string

const (
	// Client request problems.
	CodeInvalidRequest   ErrorCode = "invalid_request"
	CodeValidationFailed ErrorCode = "validation_failed"
	CodeUnsupportedMedia ErrorCode = "unsupported_media_type"
	CodeMethodNotAllowed ErrorCode = "method_not_allowed"

	// Identity and permission problems.
	CodeAuthenticationRequired ErrorCode = "authentication_required"
	CodePermissionDenied       ErrorCode = "permission_denied"

	// Resource state problems.
	CodeNotFound      ErrorCode = "not_found"
	CodeConflict      ErrorCode = "conflict"
	CodeAlreadyExists ErrorCode = "already_exists"

	// Throttling.
	CodeRateLimited ErrorCode = "rate_limited"

	// Server and dependency problems.
	CodeInternal       ErrorCode = "internal_error"
	CodeUnavailable    ErrorCode = "service_unavailable"
	CodeNotImplemented ErrorCode = "not_implemented"
)

// ErrorBody is the payload clients receive for every error response.
type ErrorBody struct {
	Code ErrorCode `json:"code"`
	// Message is human-readable and may change; clients branch on Code.
	Message string `json:"message"`
	// RequestID correlates the failure with server logs. Omitted when no request
	// identifier is in scope, which is why the acceptance contract calls it optional.
	RequestID string `json:"requestId,omitempty"`
}

// errorEnvelope keeps the payload nested so success and failure bodies never collide
// on a top-level field name.
type errorEnvelope struct {
	Error ErrorBody `json:"error"`
}

// defaultCodes maps a status to its conventional code, letting callers pass only the
// status when no more specific code applies.
var defaultCodes = map[int]ErrorCode{
	http.StatusBadRequest:           CodeInvalidRequest,
	http.StatusUnauthorized:         CodeAuthenticationRequired,
	http.StatusForbidden:            CodePermissionDenied,
	http.StatusNotFound:             CodeNotFound,
	http.StatusMethodNotAllowed:     CodeMethodNotAllowed,
	http.StatusConflict:             CodeConflict,
	http.StatusUnsupportedMediaType: CodeUnsupportedMedia,
	http.StatusUnprocessableEntity:  CodeValidationFailed,
	http.StatusTooManyRequests:      CodeRateLimited,
	http.StatusInternalServerError:  CodeInternal,
	http.StatusNotImplemented:       CodeNotImplemented,
	http.StatusServiceUnavailable:   CodeUnavailable,
}

// DefaultCode reports the conventional code for a status, falling back to an
// internal error so an unmapped status still produces a valid envelope.
func DefaultCode(status int) ErrorCode {
	if code, ok := defaultCodes[status]; ok {
		return code
	}
	if status >= 400 && status < 500 {
		return CodeInvalidRequest
	}
	return CodeInternal
}

// Write sends the stable JSON error envelope, attaching the in-scope request ID. It
// replaces http.Error for public API routes. The status determines the code.
func Write(w http.ResponseWriter, r *http.Request, status int, message string) {
	WriteCode(w, r, status, DefaultCode(status), message)
}

// Body builds the error body for a status without writing it, for responses that must
// carry the envelope alongside other fields. The status determines the code.
func Body(r *http.Request, status int, message string) ErrorBody {
	return bodyWithCode(r, status, DefaultCode(status), message)
}

func bodyWithCode(r *http.Request, status int, code ErrorCode, message string) ErrorBody {
	body := ErrorBody{Code: code, Message: message}
	if r != nil {
		body.RequestID = requestid.From(r.Context())
	}
	if message == "" {
		body.Message = http.StatusText(status)
	}
	return body
}

// WriteCode sends the envelope with an explicit code, for failures that share a status
// but need to be distinguished by clients.
func WriteCode(w http.ResponseWriter, r *http.Request, status int, code ErrorCode, message string) {
	body := bodyWithCode(r, status, code, message)

	// Marshal before committing headers: a failure here cannot be reported as JSON, so
	// fall back to plain text rather than emitting a half-written body.
	payload, err := json.Marshal(errorEnvelope{Error: body})
	if err != nil {
		http.Error(w, body.Message, status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	// Error bodies must never be sniffed or cached as another content type.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(payload)
}
