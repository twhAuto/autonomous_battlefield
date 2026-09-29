// Checks trace propagation, optional exporter setup, sampling validation, and
// HTTP/consumer/database instrumentation.

package telemetry

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestTraceContextRoundTrip(t *testing.T) {
	otel.SetTextMapPropagator(propagation.TraceContext{})
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}, TraceFlags: trace.FlagsSampled})
	ctx := trace.ContextWithSpanContext(context.Background(), spanContext)
	traceparent, tracestate := TraceContext(ctx)
	if traceparent == "" {
		t.Fatal("traceparent was not injected")
	}
	extracted := trace.SpanContextFromContext(ContextWithTrace(context.Background(), traceparent, tracestate))
	if extracted.TraceID() != spanContext.TraceID() || !extracted.IsRemote() {
		t.Fatalf("extracted span context = %#v", extracted)
	}
}

func TestConfigureTracingDisabledWithoutEndpoint(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	shutdown, err := ConfigureTracing(context.Background(), "test")
	if err != nil || shutdown == nil {
		t.Fatalf("ConfigureTracing() error = %v, shutdown nil = %t", err, shutdown == nil)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown() error = %v", err)
	}
}

func TestConfigureTracingRejectsInvalidSamplingRatio(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4318")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "invalid")
	shutdown, err := ConfigureTracing(context.Background(), "test")
	if err == nil || shutdown != nil {
		t.Fatalf("ConfigureTracing() error = %v, shutdown nil = %t", err, shutdown == nil)
	}
}

func TestConfigureTracingWithExporter(t *testing.T) {
	previous := otel.GetTracerProvider()
	t.Cleanup(func() { otel.SetTracerProvider(previous) })
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4318")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "0")
	t.Setenv("BATTLEFIELD_ENVIRONMENT", "test")
	shutdown, err := ConfigureTracing(context.Background(), "test-service")
	if err != nil || shutdown == nil {
		t.Fatalf("ConfigureTracing() error = %v, shutdown nil = %t", err, shutdown == nil)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown() error = %v", err)
	}
}

func TestEnvironment(t *testing.T) {
	t.Setenv("BATTLEFIELD_ENVIRONMENT", "")
	if got := environment(); got != "local" {
		t.Fatalf("environment() = %q", got)
	}
	t.Setenv("BATTLEFIELD_ENVIRONMENT", " staging ")
	if got := environment(); got != "staging" {
		t.Fatalf("environment() = %q", got)
	}
}

func TestHTTPInstrumentation(t *testing.T) {
	called := false
	handler := HTTPHandler("test.request", http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		called = true
		response.WriteHeader(http.StatusNoContent)
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health", nil))
	if !called || response.Code != http.StatusNoContent {
		t.Fatalf("handler called=%t status=%d", called, response.Code)
	}

	base := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusAccepted, Body: http.NoBody, Header: make(http.Header)}, nil
	})
	client := HTTPClient(base)
	result, err := client.Get("http://example.test")
	if err != nil {
		t.Fatal(err)
	}
	if err := result.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if result.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d", result.StatusCode)
	}
	if client := HTTPClient(nil); client.Transport == nil {
		t.Fatal("HTTPClient(nil) has no transport")
	}
}

// productionSampler mirrors ConfigureTracing with a ratio that never samples, so any
// recorded span was kept by an always-sample rule.
func productionSampler(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	provider := tracesdk.NewTracerProvider(tracesdk.WithSpanProcessor(recorder),
		tracesdk.WithSampler(decisionSampler{fallback: tracesdk.ParentBased(tracesdk.NeverSample())}))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = provider.Shutdown(context.Background())
	})
	return recorder
}

func TestBattleProcessIsSampledRootLinkedToQueuingRequest(t *testing.T) {
	recorder := productionSampler(t)
	// The queuing request was not sampled (flags 00).
	queued := "00-0102030405060708090a0b0c0d0e0f10-0102030405060708-00"
	ctx, process := StartBattleProcess(context.Background(), queued, "", attribute.String("battlefield.battle_id", "b"))
	_, window := StartSpan(ctx, "battlefield.engine.decision_window")
	EndSpan(window, errors.New("window failed"))
	_, database := StartDatabaseSpan(ctx, "claim")
	database.End()
	process.End()
	_, unlinked := StartBattleProcess(context.Background(), "", "")
	unlinked.End()

	spans := recorder.Ended()
	if len(spans) != 4 {
		t.Fatalf("recorded %d spans; want 4", len(spans))
	}
	windowSpan, databaseSpan, processSpan, unlinkedSpan := spans[0], spans[1], spans[2], spans[3]
	if processSpan.Name() != EngineProcessSpan || processSpan.SpanKind() != trace.SpanKindConsumer || processSpan.Parent().IsValid() {
		t.Fatalf("process span name=%q kind=%v parent=%v", processSpan.Name(), processSpan.SpanKind(), processSpan.Parent())
	}
	links := processSpan.Links()
	if len(links) != 1 || links[0].SpanContext.TraceID().String() != "0102030405060708090a0b0c0d0e0f10" || links[0].SpanContext.SpanID().String() != "0102030405060708" {
		t.Fatalf("process links = %#v", links)
	}
	if processSpan.SpanContext().TraceID() == links[0].SpanContext.TraceID() {
		t.Fatal("battle trace reused the queuing request trace")
	}
	for _, child := range []tracesdk.ReadOnlySpan{windowSpan, databaseSpan} {
		if child.Parent().SpanID() != processSpan.SpanContext().SpanID() {
			t.Fatalf("%s is not a child of the battle span", child.Name())
		}
	}
	if windowSpan.Status().Code != codes.Error || len(windowSpan.Events()) == 0 {
		t.Fatalf("window error status=%v events=%d", windowSpan.Status(), len(windowSpan.Events()))
	}
	if databaseSpan.SpanKind() != trace.SpanKindClient || databaseSpan.Name() != "postgresql.claim" {
		t.Fatalf("database span kind=%v name=%q", databaseSpan.SpanKind(), databaseSpan.Name())
	}
	if len(unlinkedSpan.Links()) != 0 {
		t.Fatal("battle without a queuing trace has a link")
	}
}

func TestBattleCreationRequestsAreAlwaysSampled(t *testing.T) {
	recorder := productionSampler(t)
	handler := HTTPHandler("battlefield-api.http", http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusAccepted)
	}))
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodPost, "/v1/battles", nil),
		httptest.NewRequest(http.MethodGet, "/v1/battles", nil),
		httptest.NewRequest(http.MethodPost, "/v1/factions", nil),
	} {
		handler.ServeHTTP(httptest.NewRecorder(), request)
	}
	spans := recorder.Ended()
	if len(spans) != 1 || spans[0].SpanKind() != trace.SpanKindServer {
		t.Fatalf("recorded %d spans; want only the POST /v1/battles server span", len(spans))
	}
	for _, a := range spans[0].Attributes() {
		if a.Key == "url.path" && a.Value.AsString() != "/v1/battles" {
			t.Fatalf("recorded path %q", a.Value.AsString())
		}
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (function roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
