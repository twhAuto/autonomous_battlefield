package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadJSONAcceptsOneValue(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"Northwind"}`))
	response := httptest.NewRecorder()
	var destination struct {
		Name string `json:"name"`
	}
	if err := ReadJSON(response, request, &destination); err != nil {
		t.Fatalf("ReadJSON() returned an error: %v", err)
	}
	if destination.Name != "Northwind" {
		t.Fatalf("decoded name = %q", destination.Name)
	}
}

func TestReadJSONRejectsInvalidOrMultipleValues(t *testing.T) {
	for _, body := range []string{"{", `{}` + "\n" + `{}`} {
		request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		if err := ReadJSON(httptest.NewRecorder(), request, &struct{}{}); err == nil {
			t.Fatalf("ReadJSON() accepted %q", body)
		}
	}
}

func TestWriteJSONCommitsPayload(t *testing.T) {
	response := httptest.NewRecorder()
	if err := WriteJSON(response, http.StatusCreated, map[string]string{"name": "Northwind"}); err != nil {
		t.Fatalf("WriteJSON() returned an error: %v", err)
	}
	if response.Code != http.StatusCreated || response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("response = %#v", response)
	}
	var decoded map[string]string
	if err := json.NewDecoder(bytes.NewReader(response.Body.Bytes())).Decode(&decoded); err != nil || decoded["name"] != "Northwind" {
		t.Fatalf("decoded response = %#v, error = %v", decoded, err)
	}
}

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
