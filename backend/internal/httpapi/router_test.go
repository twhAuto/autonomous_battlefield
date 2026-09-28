package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/apierror"
)

type routeFunc func(*http.ServeMux)

func (f routeFunc) RegisterRoutes(mux *http.ServeMux) { f(mux) }

func testRouter() http.Handler {
	return NewRouter(routeFunc(func(mux *http.ServeMux) {
		mux.HandleFunc("GET /v1/battles", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"battles":[]}`))
		})
		// A subtree pattern, so a request without the trailing slash is redirected.
		mux.HandleFunc("GET /v1/docs/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
		mux.HandleFunc("GET /public", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	}))
}

func serve(handler http.Handler, method, path string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(method, path, nil))
	return recorder
}

func envelopeOf(t *testing.T, recorder *httptest.ResponseRecorder) apierror.ErrorBody {
	t.Helper()
	var envelope struct {
		Error apierror.ErrorBody `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("response is not a JSON error envelope: %v (%q)", err, recorder.Body.String())
	}
	return envelope.Error
}

func TestUnknownAPIRouteReturnsTheEnvelope(t *testing.T) {
	recorder := serve(testRouter(), http.MethodGet, "/v1/no-such-route")

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
	body := envelopeOf(t, recorder)
	if body.Code != apierror.CodeNotFound || body.Message != "route not found" {
		t.Fatalf("error = %+v, want not_found / route not found", body)
	}
	// The router's logging middleware runs outside the fallback, so the ID is present.
	if body.RequestID == "" {
		t.Fatal("envelope is missing the request ID")
	}
}

func TestWrongMethodKeepsStatusAndAllowHeader(t *testing.T) {
	recorder := serve(testRouter(), http.MethodDelete, "/v1/battles")

	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusMethodNotAllowed)
	}
	// Clients rely on Allow to learn the supported methods; replacing the body must keep it.
	if allow := recorder.Header().Get("Allow"); allow == "" {
		t.Fatal("Allow header was dropped")
	}
	if body := envelopeOf(t, recorder); body.Code != apierror.CodeMethodNotAllowed {
		t.Fatalf("code = %q, want %q", body.Code, apierror.CodeMethodNotAllowed)
	}
}

func TestMatchedRoutesAreUntouched(t *testing.T) {
	recorder := serve(testRouter(), http.MethodGet, "/v1/battles")

	if recorder.Code != http.StatusOK || recorder.Body.String() != `{"battles":[]}` {
		t.Fatalf("got %d %q, want the handler's own response", recorder.Code, recorder.Body.String())
	}
}

func TestNonAPIPathsKeepTheMuxDefault(t *testing.T) {
	// Only /v1 is part of the documented contract; other paths are not rewritten.
	recorder := serve(testRouter(), http.MethodGet, "/no-such-page")

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType == "application/json" {
		t.Fatalf("non-API path was rewritten to JSON: %q", recorder.Body.String())
	}
}

func TestRedirectsPassThrough(t *testing.T) {
	recorder := serve(testRouter(), http.MethodGet, "/v1/docs")

	// The mux chooses the redirect status; the fallback must forward it, not rewrite it.
	if recorder.Code < 300 || recorder.Code >= 400 {
		t.Fatalf("status = %d, want a redirect", recorder.Code)
	}
	if location := recorder.Header().Get("Location"); location != "/v1/docs/" {
		t.Fatalf("Location = %q, want /v1/docs/", location)
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType == "application/json" {
		t.Fatalf("redirect was rewritten to a JSON error: %q", recorder.Body.String())
	}
}

func TestStatusProbeRecordsTheFirstStatusOnly(t *testing.T) {
	probe := &statusProbe{header: http.Header{}}
	probe.WriteHeader(http.StatusNotFound)
	probe.WriteHeader(http.StatusOK)
	if probe.status != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", probe.status, http.StatusNotFound)
	}

	implicit := &statusProbe{header: http.Header{}}
	if n, err := implicit.Write([]byte("body")); n != 4 || err != nil {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if implicit.status != http.StatusOK {
		t.Fatalf("implicit status = %d, want %d", implicit.status, http.StatusOK)
	}
}
