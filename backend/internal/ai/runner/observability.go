package runner

import (
	"context"
	"encoding/json"
	"os"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/prompts"
	"github.com/twh09/autonomous_battlefield/backend/internal/observability/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

func generationSpan(ctx context.Context, session ai.DecisionSession, scope string) (context.Context, trace.Span) {
	model := session.Profile.Model
	if session.Profile.Runtime == "api" && session.Profile.ModelEnvironmentVariable != "" {
		model = os.Getenv(session.Profile.ModelEnvironmentVariable)
	}
	promptVersion := prompts.Version
	if session.JobType == "faction_background" {
		promptVersion = prompts.GenerationVersion
	}
	return tracing.StartAI(ctx, "battlefield.model."+scope, "generation",
		attribute.String("langfuse.observation.model.name", model),
		attribute.String("langfuse.trace.metadata.profile_id", session.Profile.ID),
		attribute.String("langfuse.trace.metadata.provider", session.Profile.Provider),
		attribute.String("langfuse.observation.metadata.usage_scope", scope),
		attribute.String("langfuse.trace.metadata.prompt_version", promptVersion),
		attribute.String("langfuse.trace.metadata.tool_schema", ai.ToolSchema))
}
func finishGeneration(span trace.Span, usage ai.Usage, failed bool) {
	// Root decision spans carry no billable usage: summing observations cannot
	// double count the API calls or CLI aggregate recorded here.
	if usage.TokensReported || usage.InputTokens != 0 || usage.OutputTokens != 0 {
		details := map[string]int64{"input": usage.InputTokens, "output": usage.OutputTokens}
		// Cache reads are a subset of input. Export mutually exclusive categories.
		if usage.CachedInputTokens != nil && *usage.CachedInputTokens >= 0 && *usage.CachedInputTokens <= usage.InputTokens {
			details["input"] -= *usage.CachedInputTokens
			details["input_cached_tokens"] = *usage.CachedInputTokens
		}
		if usage.ReasoningOutputTokens != nil && *usage.ReasoningOutputTokens >= 0 && *usage.ReasoningOutputTokens <= usage.OutputTokens {
			details["output"] -= *usage.ReasoningOutputTokens
			details["output_reasoning_tokens"] = *usage.ReasoningOutputTokens
		}
		data, _ := json.Marshal(details)
		span.SetAttributes(attribute.String("langfuse.observation.usage_details", string(data)))
	}
	if usage.ReasoningOutputTokens != nil {
		span.SetAttributes(attribute.Int64("battlefield.reasoning_output_tokens", *usage.ReasoningOutputTokens))
	}
	span.SetAttributes(attribute.Bool("battlefield.tokens_reported", usage.TokensReported))
	tracing.AIOutcome(span, failed, "model_execution")
	span.End()
}
func sumOptional(total **int64, value *int64) {
	if value == nil {
		return
	}
	if *total == nil {
		*total = new(int64)
	}
	**total += *value
}
