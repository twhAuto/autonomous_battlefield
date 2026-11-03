package runner

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/observability/tracing"
	"go.opentelemetry.io/otel"
	sdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestGenerationUsageDoesNotDoubleCountCache(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdk.NewTracerProvider(sdk.WithSpanProcessor(recorder))
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { otel.SetTracerProvider(old); _ = provider.Shutdown(context.Background()) })
	ctx := tracing.DecisionContext(context.Background(), "request")
	_, span := generationSpan(ctx, ai.DecisionSession{Profile: ai.ModelProfile{Model: "test-model"}}, "cli_session")
	cached, reasoning := int64(20), int64(4)
	finishGeneration(span, ai.Usage{TokensReported: true, InputTokens: 30, OutputTokens: 10, CachedInputTokens: &cached, ReasoningOutputTokens: &reasoning}, false)
	var details map[string]int64
	for _, attr := range recorder.Ended()[0].Attributes() {
		if attr.Key == "langfuse.observation.usage_details" {
			_ = json.Unmarshal([]byte(attr.Value.AsString()), &details)
		}
	}
	if details["input"] != 10 || details["input_cached_tokens"] != 20 || details["output"] != 6 || details["output_reasoning_tokens"] != 4 || len(details) != 4 {
		t.Fatal(details)
	}
	_, missing := generationSpan(ctx, ai.DecisionSession{}, "cli_session")
	finishGeneration(missing, ai.Usage{}, true)
	for _, attr := range recorder.Ended()[1].Attributes() {
		if attr.Key == "langfuse.observation.usage_details" {
			t.Fatal("invented unknown token usage")
		}
	}
}

// Traces name the model a run used: a frozen API option, otherwise the environment model.
func TestGenerationSpanNamesTheModelThatRan(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdk.NewTracerProvider(sdk.WithSpanProcessor(recorder))
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { otel.SetTracerProvider(old); _ = provider.Shutdown(context.Background()) })
	t.Setenv("TRACE_API_MODEL", "environment-model")
	for index, test := range []struct{ model, want string }{{"chosen-option", "chosen-option"}, {"", "environment-model"}} {
		profile := ai.ModelProfile{Runtime: "api", ModelEnvironmentVariable: "TRACE_API_MODEL", Model: test.model}
		_, span := generationSpan(context.Background(), ai.DecisionSession{Profile: profile}, "api_turn")
		span.End()
		name := ""
		for _, attr := range recorder.Ended()[index].Attributes() {
			if attr.Key == "langfuse.observation.model.name" {
				name = attr.Value.AsString()
			}
		}
		if name != test.want {
			t.Fatalf("model name = %q, want %q", name, test.want)
		}
	}
}

type failedUsageProvider struct{}

func (failedUsageProvider) NextTurn(context.Context, ai.ModelTurnRequest) (ai.ModelTurnResult, error) {
	cached := int64(2)
	return ai.ModelTurnResult{Usage: ai.Usage{TokensReported: true, InputTokens: 8, OutputTokens: 3, CachedInputTokens: &cached}}, errors.New("refusal")
}
func TestAPIFailureRetainsReportedUsage(t *testing.T) {
	result, err := (APIRunner{Provider: failedUsageProvider{}, Connector: fakeConnector{&fakeToolSession{}}}).Run(context.Background(), ai.DecisionSession{Profile: ai.ModelProfile{MaxSteps: 3}})
	if err == nil || result.Usage.InputTokens != 8 || result.Usage.OutputTokens != 3 || *result.Usage.ModelCalls != 1 || *result.Usage.CachedInputTokens != 2 {
		t.Fatal(result, err)
	}
}
