// Checks request correlation IDs, response logging, and bounded route labels for HTTP
// metrics.

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/authn"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/observability/metrics"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/observability/tracing"
	"go.opentelemetry.io/otel"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
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
	}), nil, logger)

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
	}), nil, logger)
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
		}), nil, nil)
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
	for _, accepted := range []string{"a", "req-1.2_3:4", strings.Repeat("x", 128)} {
		if got := acceptedRequestID(accepted); got != accepted {
			t.Fatalf("acceptedRequestID(%q) = %q, want it kept", accepted, got)
		}
	}
	generated := regexp.MustCompile(`^[0-9a-f]{32}$`)
	for _, rejected := range []string{"", " padded ", strings.Repeat("x", 129), "has space", `quote"`, "naïve", "line\nbreak"} {
		if got := acceptedRequestID(rejected); got == rejected || !generated.MatchString(got) {
			t.Fatalf("acceptedRequestID(%q) = %q, want a generated 32-hex ID", rejected, got)
		}
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
	handler := requestLogging(mux, mux, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))
	handler.ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/v1/battles/battle-specific-id", nil),
	)

	response := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(
		response,
		httptest.NewRequest(http.MethodGet, "/metrics", nil),
	)
	metric := `battlefield_http_server_requests_total{code="200",method="GET",route="GET /v1/battles/{id}"}`
	if !strings.Contains(response.Body.String(), metric) {
		t.Fatalf("metrics do not contain normalized route %q", metric)
	}
}

// Router middleware hands the mux a copy of the request, and authentication rejects
// some requests before the mux sees them. Neither may cost a request its route in
// the metric label or on the server span.
func TestRouteLabelSurvivesMiddlewareAndEarlyRejection(t *testing.T) {
	spans := tracetest.NewSpanRecorder()
	provider := tracesdk.NewTracerProvider(tracesdk.WithSpanProcessor(spans))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = provider.Shutdown(context.Background())
	})
	routes := routeFunc(func(mux *http.ServeMux) {
		mux.HandleFunc("GET /v1/route-labels/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	})

	for _, test := range []struct {
		name    string
		allowed bool
		path    string
		code    int
		route   string
	}{
		{"authenticated", true, "/v1/route-labels/battle-1", http.StatusOK, "GET /v1/route-labels/{id}"},
		{"rejected before routing", false, "/v1/route-labels/battle-2", http.StatusUnauthorized, "GET /v1/route-labels/{id}"},
		{"unregistered", true, "/v1/route-labels-missing", http.StatusNotFound, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			authenticate := func(next http.Handler) http.Handler {
				return authn.RequireAuthentication(true, auditAuthenticator{test.allowed}, next)
			}
			handler := tracing.HTTPHandler("test.api", NewRouterWithMiddleware([]Middleware{authenticate}, routes))
			label := test.route
			if label == "" {
				label = "unmatched"
			}
			series := fmt.Sprintf(`battlefield_http_server_requests_total{code="%d",method="GET",route="%s"}`, test.code, label)
			before := requestCount(t, series)

			if code := serve(handler, http.MethodGet, test.path).Code; code != test.code {
				t.Fatalf("status = %d, want %d", code, test.code)
			}
			if delta := requestCount(t, series) - before; delta != 1 {
				t.Fatalf("%s increased by %v, want 1", series, delta)
			}
			ended := spans.Ended()
			if route := spanAttribute(ended[len(ended)-1], "http.route"); route != test.route {
				t.Fatalf("span http.route = %q, want %q", route, test.route)
			}
		})
	}
}

// requestCount reads one HTTP request counter series from the metrics endpoint, or 0
// when the series does not exist yet.
func requestCount(t *testing.T, series string) float64 {
	t.Helper()
	response := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, line := range strings.Split(response.Body.String(), "\n") {
		if value, found := strings.CutPrefix(line, series+" "); found {
			count, err := strconv.ParseFloat(value, 64)
			if err != nil {
				t.Fatal(err)
			}
			return count
		}
	}
	return 0
}

func spanAttribute(span tracesdk.ReadOnlySpan, key string) string {
	for _, attribute := range span.Attributes() {
		if string(attribute.Key) == key {
			return attribute.Value.AsString()
		}
	}
	return ""
}

type auditAuthenticator struct {
	allowed bool
}

func (a auditAuthenticator) Authenticate(*http.Request) (authn.Actor, bool) {
	return authn.Actor{ID: "verified-issuer|subject", Role: authn.Player}, a.allowed
}

func TestRequestLoggingUsesVerifiedActorAcrossMiddleware(t *testing.T) {
	for _, allowed := range []bool{false, true} {
		var output bytes.Buffer
		handler := requestLogging(authn.RequireAuthentication(true, auditAuthenticator{allowed}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// A denied ownership check must still identify the authenticated caller.
			w.WriteHeader(http.StatusForbidden)
		})), nil, slog.New(slog.NewJSONHandler(&output, nil)))
		request := httptest.NewRequest(http.MethodDelete, "/v1/factions/other", nil)
		request.Header.Set("Cf-Access-Authenticated-User-Email", "forged@example.com")
		request.Header.Set("X-Actor-ID", "forged-admin")
		handler.ServeHTTP(httptest.NewRecorder(), request)
		var entry map[string]any
		if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
			t.Fatal(err)
		}
		wantID, wantRole, wantStatus := "", "", float64(http.StatusUnauthorized)
		if allowed {
			wantID, wantRole, wantStatus = "verified-issuer|subject", "player", float64(http.StatusForbidden)
		}
		if entry["actor_id"] != wantID || entry["actor_role"] != wantRole || entry["status"] != wantStatus {
			t.Fatalf("allowed=%v: unexpected audit entry: %#v", allowed, entry)
		}
		if strings.Contains(output.String(), "forged") {
			t.Fatal("unverified identity leaked into audit log")
		}
	}
}

func TestCloudflareFailureLogDoesNotExposeCredentials(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	manager := authn.NewManager(authn.Config{Mode: authn.ModeCloudflare})
	mux := http.NewServeMux()
	authn.NewRoutes(manager).RegisterRoutes(mux)
	request := httptest.NewRequest("GET", "/v1/auth/session", nil)
	request.Header.Set("X-Request-ID", "auth-diagnostic-test")
	request.Header.Set("Cf-Access-Jwt-Assertion", "secret-token")
	request.Header.Set("Cf-Access-Client-Secret", "secret-client-secret")
	request.AddCookie(&http.Cookie{Name: "CF_Authorization", Value: "secret-cookie"})
	response := httptest.NewRecorder()
	requestLogging(mux, mux, logger).ServeHTTP(response, request)
	if response.Code != 401 {
		t.Fatal(response.Code)
	}
	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	if event["auth_failure"] != "token_format_invalid" || event["request_id"] != "auth-diagnostic-test" {
		t.Fatal(event)
	}
	for _, secret := range []string{"secret-token", "secret-client-secret", "secret-cookie"} {
		if strings.Contains(output.String(), secret) || strings.Contains(response.Body.String(), secret) {
			t.Fatal("credential leaked")
		}
	}
	if strings.Contains(response.Body.String(), "token_format_invalid") {
		t.Fatal("reason leaked to HTTP response")
	}
}
