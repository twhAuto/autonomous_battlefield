// Decision-scoped AI observations use stable IDs across the worker and scoped MCP
// server. No prompts, capabilities, model reasoning, or raw tool payloads are exported.
package tracing

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"net/url"
	"os"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type decisionMetadataKey struct{}
type decisionKey struct{}
type decisionRootKey struct{}

func decisionIDs(id string) (trace.TraceID, trace.SpanID) {
	sum := sha256.Sum256([]byte("battlefield.ai.v1:" + id))
	var tid trace.TraceID
	var sid trace.SpanID
	copy(tid[:], sum[:16])
	copy(sid[:], sum[16:24])
	return tid, sid
}

// DecisionContext derives correlation only from the authenticated capability's
// request ID, never a client-supplied trace header or tool argument.
func DecisionContext(ctx context.Context, requestID string) context.Context {
	tid, sid := decisionIDs(requestID)
	ctx = context.WithValue(ctx, decisionKey{}, requestID)
	return trace.ContextWithRemoteSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled, Remote: true,
	}))
}

func StartDecision(ctx context.Context, requestID string) (context.Context, trace.Span) {
	ctx = context.WithValue(ctx, decisionKey{}, requestID)
	ctx = context.WithValue(ctx, decisionRootKey{}, true)
	options := []trace.SpanStartOption{trace.WithNewRoot(), trace.WithAttributes(aiAttributes(ctx, "agent")...)}
	// The decision keeps its own deterministic trace for Langfuse; a link leads back to
	// the engine decision window that requested it.
	if window := trace.SpanContextFromContext(ctx); window.IsValid() {
		options = append(options, trace.WithLinks(trace.Link{SpanContext: window}))
	}
	ctx, span := otel.Tracer(instrumentationName).Start(ctx, "battlefield.agent.decision", options...)
	return context.WithValue(ctx, decisionRootKey{}, false), span
}

func StartAI(ctx context.Context, name, kind string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return otel.Tracer(instrumentationName).Start(ctx, name, trace.WithAttributes(append(aiAttributes(ctx, kind), attrs...)...))
}

func WithDecisionMetadata(ctx context.Context, attrs ...attribute.KeyValue) context.Context {
	return context.WithValue(ctx, decisionMetadataKey{}, attrs)
}

func aiAttributes(ctx context.Context, kind string) []attribute.KeyValue {
	id, _ := ctx.Value(decisionKey{}).(string)
	attrs, _ := ctx.Value(decisionMetadataKey{}).([]attribute.KeyValue)
	return append(append([]attribute.KeyValue{}, attrs...), []attribute.KeyValue{
		attribute.String("langfuse.observation.type", kind),
		attribute.String("langfuse.trace.name", "battlefield.decision"),
		attribute.String("langfuse.trace.metadata.request_id", id),
		attribute.String("langfuse.environment", environment()),
	}...)
}

func AIOutcome(span trace.Span, failed bool, category string) {
	span.SetAttributes(attribute.Bool("battlefield.failed", failed))
	if failed {
		span.SetStatus(codes.Error, category)
	}
}

func DecisionTraceID(requestID string) string { tid, _ := decisionIDs(requestID); return tid.String() }

func DecisionTraceURL(requestID string) string {
	base, err := url.Parse(strings.TrimSpace(os.Getenv("LANGFUSE_BASE_URL")))
	project := strings.TrimSpace(os.Getenv("LANGFUSE_PROJECT_ID"))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.User != nil || project == "" {
		return ""
	}
	base.RawQuery = ""
	base.Fragment = ""
	return strings.TrimRight(base.String(), "/") + "/project/" + url.PathEscape(project) + "/traces/" + DecisionTraceID(requestID)
}

type decisionSampler struct{ fallback sdk.Sampler }

// ShouldSample keeps every AI decision, battle-creation request, and battle attempt:
// together they are low volume and form the end-to-end battle trace. Other spans use
// the fallback ratio.
func (s decisionSampler) ShouldSample(p sdk.SamplingParameters) sdk.SamplingResult {
	if p.Name == EngineProcessSpan || battleCreation(p) {
		return sdk.SamplingResult{Decision: sdk.RecordAndSample}
	}
	for _, a := range p.Attributes {
		if a.Key == "langfuse.observation.type" {
			return sdk.SamplingResult{Decision: sdk.RecordAndSample}
		}
	}
	return s.fallback.ShouldSample(p)
}
func (s decisionSampler) Description() string {
	return "AI decisions and battles always; " + s.fallback.Description()
}

// battleCreation matches the API server span for POST /v1/battles by the attributes
// otelhttp sets when the span starts.
func battleCreation(p sdk.SamplingParameters) bool {
	if p.Kind != trace.SpanKindServer {
		return false
	}
	var method, path string
	for _, a := range p.Attributes {
		switch a.Key {
		case "http.request.method":
			method = a.Value.AsString()
		case "url.path":
			path = a.Value.AsString()
		}
	}
	return method == "POST" && path == "/v1/battles"
}

type decisionIDGenerator struct{}

func (decisionIDGenerator) NewIDs(ctx context.Context) (trace.TraceID, trace.SpanID) {
	if root, _ := ctx.Value(decisionRootKey{}).(bool); root {
		id, _ := ctx.Value(decisionKey{}).(string)
		return decisionIDs(id)
	}
	var tid trace.TraceID
	_, _ = rand.Read(tid[:])
	return tid, decisionIDGenerator{}.NewSpanID(ctx, tid)
}
func (decisionIDGenerator) NewSpanID(_ context.Context, _ trace.TraceID) trace.SpanID {
	var sid trace.SpanID
	_, _ = rand.Read(sid[:])
	return sid
}
