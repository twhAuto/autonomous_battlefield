package mcpserver

import (
	"context"
	"encoding/json"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai/agentview"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

func recordContextSize(ctx context.Context, value ai.DecisionContext) {
	span := trace.SpanFromContext(ctx)
	payload, _ := json.Marshal(agentview.Observation(value))
	span.SetAttributes(attribute.Int("battlefield.context.bytes", len(payload)),
		attribute.String("langfuse.session.id", value.BattleID),
		attribute.String("langfuse.trace.metadata.battle_id", value.BattleID),
		attribute.String("langfuse.trace.metadata.faction_id", value.FactionID),
		attribute.Int64("langfuse.trace.metadata.target_tick", int64(value.TargetTick)),
		attribute.String("langfuse.trace.metadata.context_schema", value.SchemaVersion))
	var sections map[string]json.RawMessage
	_ = json.Unmarshal(payload, &sections)
	for _, name := range []string{"view", "legal", "knowledge", "memory", "economy", "referenceUnits", "activePlan"} {
		span.SetAttributes(attribute.Int("battlefield.context.bytes."+name, len(sections[name])))
	}
}
