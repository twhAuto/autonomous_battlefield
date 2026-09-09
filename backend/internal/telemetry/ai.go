// Decision-scoped AI observations use stable IDs across the worker and scoped MCP
// server. No prompts, capabilities, model reasoning, or raw tool payloads are exported.
package telemetry

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
	ctx, span := otel.Tracer(instrumentationName).Start(ctx, "battlefield.agent.decision", trace.WithNewRoot(), trace.WithAttributes(aiAttributes(ctx, "agent")...))
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

func (s decisionSampler) ShouldSample(p sdk.SamplingParameters) sdk.SamplingResult {
	for _, a := range p.Attributes {
		if a.Key == "langfuse.observation.type" {
			return sdk.SamplingResult{Decision: sdk.RecordAndSample}
		}
	}
	return s.fallback.ShouldSample(p)
}
func (s decisionSampler) Description() string {
	return "AI decisions always; " + s.fallback.Description()
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
