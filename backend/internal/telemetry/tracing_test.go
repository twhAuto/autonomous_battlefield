package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
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

func TestConsumerAndDatabaseSpans(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := tracesdk.NewTracerProvider(tracesdk.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = provider.Shutdown(context.Background())
	})

	_, consumer := StartConsumerSpan(context.Background(), "queue.claim", attribute.String("messaging.operation", "receive"))
	consumer.End()
	_, database := StartDatabaseSpan(context.Background(), "claim")
	database.End()
	spans := recorder.Ended()
	if len(spans) != 2 {
		t.Fatalf("ended spans = %d", len(spans))
	}
	if spans[0].SpanKind() != trace.SpanKindConsumer || spans[0].Name() != "queue.claim" {
		t.Fatalf("consumer span kind=%v name=%q", spans[0].SpanKind(), spans[0].Name())
	}
	if spans[1].SpanKind() != trace.SpanKindClient || spans[1].Name() != "postgresql.claim" {
		t.Fatalf("database span kind=%v name=%q", spans[1].SpanKind(), spans[1].Name())
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (function roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
