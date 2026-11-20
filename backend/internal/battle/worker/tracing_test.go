package worker

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai"
	battle "github.com/twhAuto/autonomous_battlefield/backend/internal/game"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// Agent decision traces link to the context stored on each decision, so it must be the
// decision window span rather than the whole battle span.
func TestDecisionsStoreTheirWindowSpanContext(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := tracesdk.NewTracerProvider(tracesdk.WithSpanProcessor(recorder))
	previous, previousPropagator := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		otel.SetTextMapPropagator(previousPropagator)
		_ = provider.Shutdown(context.Background())
	})

	job := testJob(20)
	for index := range job.Scenario.Factions {
		job.Scenario.Factions[index].Controller = battle.ControllerConfig{Type: ai.ControllerScripted}
	}
	simulation, err := battle.NewMatch(job.Lease.BattleID, job.Scenario, job.RequestedTicks)
	if err != nil {
		t.Fatal(err)
	}
	for range 9 {
		if _, err := simulation.Advance(); err != nil {
			t.Fatal(err)
		}
	}
	repository := &decisionBattleRepository{fakeBattleRepository: &fakeBattleRepository{}, records: map[string]ai.DecisionRecord{}}
	worker, _ := NewWithLogger(repository, repository, testWorkerConfig(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if _, err := worker.resolveDecisionWindow(context.Background(), job.Lease, simulation); err != nil {
		t.Fatal(err)
	}

	var window tracesdk.ReadOnlySpan
	for _, span := range recorder.Ended() {
		if span.Name() == "battlefield.engine.decision_window" {
			window = span
		}
	}
	if window == nil {
		t.Fatal("no decision window span")
	}
	if len(repository.traceparents) != len(job.Scenario.Factions) {
		t.Fatalf("stored %d decision trace contexts; want %d", len(repository.traceparents), len(job.Scenario.Factions))
	}
	for _, traceparent := range repository.traceparents {
		if !strings.Contains(traceparent, window.SpanContext().SpanID().String()) {
			t.Fatalf("decision traceparent %q does not point at the window span %s", traceparent, window.SpanContext().SpanID())
		}
	}
}
