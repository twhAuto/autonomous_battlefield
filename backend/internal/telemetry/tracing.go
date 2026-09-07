// Configures optional OpenTelemetry export and carries trace context through HTTP
// requests, database work, and queued jobs.

package telemetry

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const instrumentationName = "github.com/twh09/autonomous_battlefield/backend"

func ConfigureTracing(ctx context.Context, service string) (func(context.Context) error, error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	if strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")) == "" && strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")) == "" {
		return func(context.Context) error { return nil }, nil
	}
	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, err
	}
	ratio := 0.1
	if raw := strings.TrimSpace(os.Getenv("OTEL_TRACES_SAMPLER_ARG")); raw != "" {
		parsed, parseErr := strconv.ParseFloat(raw, 64)
		if parseErr != nil || parsed < 0 || parsed > 1 {
			return nil, errors.New("OTEL_TRACES_SAMPLER_ARG must be between 0 and 1")
		}
		ratio = parsed
	}
	serviceResource, err := resource.New(ctx, resource.WithAttributes(
		attribute.String("service.name", service),
		attribute.String("deployment.environment", environment()),
	))
	if err != nil {
		return nil, err
	}
	provider := tracesdk.NewTracerProvider(
		tracesdk.WithBatcher(exporter, tracesdk.WithBatchTimeout(time.Second)),
		tracesdk.WithSampler(tracesdk.ParentBased(tracesdk.TraceIDRatioBased(ratio))),
		tracesdk.WithResource(serviceResource),
	)
	otel.SetTracerProvider(provider)
	return provider.Shutdown, nil
}

func environment() string {
	if value := strings.TrimSpace(os.Getenv("BATTLEFIELD_ENVIRONMENT")); value != "" {
		return value
	}
	return "local"
}

func HTTPHandler(operation string, handler http.Handler) http.Handler {
	return otelhttp.NewHandler(handler, operation)
}

func HTTPClient(base http.RoundTripper) *http.Client {
	if base == nil {
		base = http.DefaultTransport
	}
	return &http.Client{Transport: otelhttp.NewTransport(base)}
}

func TraceContext(ctx context.Context) (string, string) {
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	return carrier.Get("traceparent"), carrier.Get("tracestate")
}

func ContextWithTrace(ctx context.Context, traceparent, tracestate string) context.Context {
	carrier := propagation.MapCarrier{"traceparent": traceparent, "tracestate": tracestate}
	return otel.GetTextMapPropagator().Extract(ctx, carrier)
}

func StartConsumerSpan(ctx context.Context, name string, attributes ...attribute.KeyValue) (context.Context, trace.Span) {
	return otel.Tracer(instrumentationName).Start(ctx, name, trace.WithSpanKind(trace.SpanKindConsumer), trace.WithAttributes(attributes...))
}

// StartDatabaseSpan records the operation name without capturing SQL text,
// arguments, observations, credentials, or other potentially sensitive data.
func StartDatabaseSpan(ctx context.Context, operation string) (context.Context, trace.Span) {
	return otel.Tracer(instrumentationName).Start(ctx, "postgresql."+operation,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String("db.system", "postgresql"), attribute.String("db.operation.name", operation)),
	)
}
