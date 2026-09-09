package telemetry

import (
	"context"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"strings"
	"testing"
)

func TestDecisionCorrelationAndSampling(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	sampler := decisionSampler{fallback: sdk.NeverSample()}
	provider := sdk.NewTracerProvider(sdk.WithSpanProcessor(recorder), sdk.WithSampler(sampler), sdk.WithIDGenerator(decisionIDGenerator{}))
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { otel.SetTracerProvider(old); _ = provider.Shutdown(context.Background()) })
	// An unsampled upstream request must not drop AI decisions.
	upstream := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{1}}))
	ctx, root := StartDecision(upstream, "request-a")
	_, generation := StartAI(ctx, "model", "generation")
	_, tool := StartAI(DecisionContext(context.Background(), "request-a"), "lookup", "tool")
	AIOutcome(tool, true, "tool_rejected")
	tool.End()
	generation.End()
	root.End()
	spans := recorder.Ended()
	if len(spans) != 3 {
		t.Fatalf("sampled %d spans", len(spans))
	}
	for _, span := range spans {
		if span.SpanContext().TraceID() != root.SpanContext().TraceID() {
			t.Fatal("split decision trace")
		}
	}
	if spans[0].Parent().SpanID() != root.SpanContext().SpanID() || spans[1].Parent().SpanID() != root.SpanContext().SpanID() {
		t.Fatal("incorrect parent")
	}
	if spans[0].Status().Code != codes.Error {
		t.Fatal("tool failure missing")
	}
	if root.SpanContext().TraceID().String() != DecisionTraceID("request-a") || DecisionTraceID("request-a") == DecisionTraceID("request-b") {
		t.Fatal("request isolation lost")
	}
	if root.SpanContext().TraceID() == trace.SpanContextFromContext(upstream).TraceID() {
		t.Fatal("decision reused battle trace")
	}
	if sampler.ShouldSample(sdk.SamplingParameters{Attributes: []attribute.KeyValue{}}).Decision != sdk.Drop {
		t.Fatal("infra sampling changed")
	}
	if !strings.Contains(sampler.Description(), "AI decisions") {
		t.Fatal("sampler description")
	}
	// Non-AI root IDs remain random.
	a, _ := (decisionIDGenerator{}).NewIDs(context.Background())
	b, _ := (decisionIDGenerator{}).NewIDs(context.Background())
	if a == b || !a.IsValid() {
		t.Fatal("invalid infrastructure IDs")
	}
}

func TestDecisionTraceURL(t *testing.T) {
	t.Setenv("LANGFUSE_PROJECT_ID", "battlefield-agents")
	for _, base := range []string{"", "javascript:alert(1)", "https://user:password@host", "%", "https:///"} {
		t.Setenv("LANGFUSE_BASE_URL", base)
		if DecisionTraceURL("r") != "" {
			t.Fatalf("unsafe URL %s", base)
		}
	}
	t.Setenv("LANGFUSE_BASE_URL", "http://localhost:13000/")
	if got := DecisionTraceURL("r"); got != "http://localhost:13000/project/battlefield-agents/traces/"+DecisionTraceID("r") {
		t.Fatal(got)
	}
	t.Setenv("LANGFUSE_PROJECT_ID", "")
	if DecisionTraceURL("r") != "" {
		t.Fatal("missing project")
	}
}
