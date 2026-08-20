package httpapi

import (
	"net/http/httptest"
	"testing"
)

func TestWriteJSONDoesNotCommitOnEncodingError(t *testing.T) {
	response := httptest.NewRecorder()

	err := WriteJSON(response, 200, make(chan struct{}))
	if err == nil {
		t.Fatal("writeJSON() returned nil, want an encoding error")
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "" {
		t.Fatalf("Content-Type = %q, want no committed JSON headers", contentType)
	}
	if response.Body.Len() != 0 {
		t.Fatalf("response body = %q, want empty body", response.Body.String())
	}
}
