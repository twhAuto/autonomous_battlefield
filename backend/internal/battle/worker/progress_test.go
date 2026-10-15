// Checks progress-log throttling, production log formatting, pending-decision reporting,
// and lease renewal while waiting.

package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/battle/queue"
	"github.com/twh09/autonomous_battlefield/backend/internal/observability/logging"
	"go.opentelemetry.io/otel/trace"
)

func TestProgressLogRateLimit(t *testing.T) {
	var progress progressLog
	start := time.Now()
	if !progress.due(start) || progress.due(start.Add(14*time.Second)) || !progress.due(start.Add(15*time.Second)) || progress.due(start.Add(16*time.Second)) {
		t.Fatal("progress logs not bounded to one per interval")
	}
}

func TestBattleProgressUsesProductionLogger(t *testing.T) {
	output, span, err := processWithProductionLogger(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(output.String(), `"event":"battle_progress"`) != 1 {
		t.Fatal(output.String())
	}
	if strings.Contains(output.String(), "battle_tick_completed") {
		t.Fatal("per-tick debug logs leaked at INFO")
	}
	for _, line := range bytes.Split(bytes.TrimSpace(output.Bytes()), []byte("\n")) {
		checkProgressRow(t, line, span.TraceID().String())
	}
}

func TestBattleProgressIsNotReportedForAFailedSave(t *testing.T) {
	output, _, err := processWithProductionLogger(t, errWorkerRepository)
	if !errors.Is(err, errWorkerRepository) || strings.Contains(output.String(), "battle_progress") {
		t.Fatalf("reported progress for failed save: %v %s", err, output.String())
	}
}

// processWithProductionLogger runs a three-tick battle through the production log
// handler, with saves failing with saveErr when it is set.
func processWithProductionLogger(t *testing.T, saveErr error) (*bytes.Buffer, trace.SpanContext, error) {
	t.Helper()
	var output bytes.Buffer
	logger := slog.New(logging.NewHandler(slog.NewJSONHandler(&output, nil))).With("service", "engine")
	repo := &fakeBattleRepository{job: testJob(3), saveErr: saveErr}
	worker, err := NewWithLogger(repo, nil, testWorkerConfig(), logger)
	if err != nil {
		t.Fatal(err)
	}
	span := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}})
	err = worker.process(trace.ContextWithSpanContext(context.Background(), span), repo.job)
	return &output, span, err
}

// checkProgressRow requires a log line to carry the battle, trace and elapsed time, and
// a progress line to report the first of three ticks.
func checkProgressRow(t *testing.T, line []byte, traceID string) {
	t.Helper()
	var row map[string]any
	if err := json.Unmarshal(line, &row); err != nil {
		t.Fatal(err)
	}
	if row["battle_id"] != "battle-1" || row["trace_id"] != traceID {
		t.Fatal(row)
	}
	if row["event"] == "battle_progress" && (row["current_tick"] != float64(1) || row["requested_ticks"] != float64(3)) {
		t.Fatal(row)
	}
	if row["elapsed_ms"] == nil {
		t.Fatal("missing elapsed time", row)
	}
}

func TestDecisionWaitingLogsPendingCountAndRenewsLease(t *testing.T) {
	var output bytes.Buffer
	repo := &decisionBattleRepository{fakeBattleRepository: &fakeBattleRepository{}, records: map[string]ai.DecisionRecord{
		"a": {ID: "a", TargetTick: 10, Status: "pending"}, "b": {ID: "b", TargetTick: 10, Status: "running"}, "c": {ID: "c", TargetTick: 10, Status: "accepted"},
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	repo.onHeartbeat = cancel
	config := testWorkerConfig()
	config.HeartbeatInterval = 150 * time.Millisecond
	config.DecisionTimeout = time.Second
	worker, err := NewWithLogger(repo, repo, config, slog.New(logging.NewHandler(slog.NewJSONHandler(&output, nil))))
	if err != nil {
		t.Fatal(err)
	}
	err = worker.waitForWindowDecisions(ctx, queue.Lease{BattleID: "battle-1", Attempt: 1}, 10)
	if !errors.Is(err, context.Canceled) || repo.heartbeats != 1 {
		t.Fatalf("lease not renewed while waiting: %v, heartbeats=%d", err, repo.heartbeats)
	}
	var row map[string]any
	if err := json.Unmarshal(output.Bytes(), &row); err != nil {
		t.Fatalf("expected one waiting update: %v %s", err, output.String())
	}
	if row["event"] != "decision_window_waiting" || row["pending_count"] != float64(2) || !strings.Contains(row["msg"].(string), "Waiting for faction decisions") {
		t.Fatal(row)
	}
}

func TestResolvedDecisionWindowDoesNotReportWaiting(t *testing.T) {
	var output bytes.Buffer
	repo := &decisionBattleRepository{fakeBattleRepository: &fakeBattleRepository{}, records: map[string]ai.DecisionRecord{"a": {Status: "accepted", TargetTick: 10}}}
	worker, err := NewWithLogger(repo, repo, testWorkerConfig(), slog.New(logging.NewHandler(slog.NewJSONHandler(&output, nil))))
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.waitForWindowDecisions(context.Background(), queue.Lease{}, 10); err != nil {
		t.Fatal(err)
	}
	if output.Len() != 0 {
		t.Fatal("logged waiting when no decisions pending", output.String())
	}
}
