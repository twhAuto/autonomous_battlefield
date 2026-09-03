package telemetry

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
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
