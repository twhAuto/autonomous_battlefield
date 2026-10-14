// Checks progress-log throttling, production log formatting, pending-decision reporting,
// and lease renewal while waiting.

package worker

import (
	"github.com/twh09/autonomous_battlefield/backend/internal/battle/queue"
	"github.com/twh09/autonomous_battlefield/backend/internal/testutil"

	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/logging"
	"go.opentelemetry.io/otel/trace"
)

func TestProgressLogRateLimit(t *testing.T) {
	var progress progressLog
	start := time.Now()
	testutil.Require(t, !(!progress.due(start) || progress.due(start.Add(14*time.Second)) || !progress.due(start.Add(15*time.Second)) || progress.due(start.Add(16*time.Second))), func() { t.Fatal("progress logs not bounded to one per interval") })
}

func TestBattleProgressUsesProductionLogger(t *testing.T) {
	for _, failSave := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "save failure"}[failSave], func(t *testing.T) {
			var output bytes.Buffer
			logger := slog.New(logging.NewHandler(slog.NewJSONHandler(&output, nil))).With("service", "engine")
			repo := &fakeBattleRepository{job: testJob(3)}
			if failSave {
				repo.saveErr = errWorkerRepository
			}
			worker, err := NewWithLogger(repo, nil, testWorkerConfig(), logger)
			testutil.Require(t, err == nil, func() { t.Fatal(err) })
			span := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}})
			err = worker.process(trace.ContextWithSpanContext(context.Background(), span), repo.job)
			if failSave {
				testutil.Require(t, !(!errors.Is(err, errWorkerRepository) || strings.Contains(output.String(), "battle_progress")), func() { t.Fatalf("reported progress for failed save: %v %s", err, output.String()) })
				return
			}
			testutil.Require(t, err == nil, func() { t.Fatal(err) })
			testutil.Require(t, strings.Count(output.String(), `"event":"battle_progress"`) == 1, func() { t.Fatal(output.String()) })
			testutil.Require(t, !(strings.Contains(output.String(), "battle_tick_completed")), func() { t.Fatal("per-tick debug logs leaked at INFO") })
			for _, line := range bytes.Split(bytes.TrimSpace(output.Bytes()), []byte("\n")) {
				var row map[string]any
				{
					err := json.Unmarshal(line, &row)
					testutil.Require(t, err == nil, func() { t.Fatal(err) })
				}
				testutil.Require(t, !(row["battle_id"] != "battle-1" || row["trace_id"] != span.TraceID().String()), func() { t.Fatal(row) })
				testutil.Require(t, !(row["event"] == "battle_progress" && (row["current_tick"] != float64(1) || row["requested_ticks"] != float64(3))), func() { t.Fatal(row) })
				testutil.Require(t, row["elapsed_ms"] != nil, func() { t.Fatal("missing elapsed time", row) })
			}
		})
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
	testutil.Require(t, err == nil, func() { t.Fatal(err) })
	err = worker.waitForWindowDecisions(ctx, queue.Lease{BattleID: "battle-1", Attempt: 1}, 10)
	testutil.Require(t, !(!errors.Is(err, context.Canceled) || repo.heartbeats != 1), func() { t.Fatalf("lease not renewed while waiting: %v, heartbeats=%d", err, repo.heartbeats) })
	var row map[string]any
	{
		err := json.Unmarshal(output.Bytes(), &row)
		testutil.Require(t, err == nil, func() { t.Fatalf("expected one waiting update: %v %s", err, output.String()) })
	}
	testutil.Require(t, !(row["event"] != "decision_window_waiting" || row["pending_count"] != float64(2) || !strings.Contains(row["msg"].(string), "Waiting for faction decisions")), func() { t.Fatal(row) })
}

func TestResolvedDecisionWindowDoesNotReportWaiting(t *testing.T) {
	var output bytes.Buffer
	repo := &decisionBattleRepository{fakeBattleRepository: &fakeBattleRepository{}, records: map[string]ai.DecisionRecord{"a": {Status: "accepted", TargetTick: 10}}}
	worker, err := NewWithLogger(repo, repo, testWorkerConfig(), slog.New(logging.NewHandler(slog.NewJSONHandler(&output, nil))))
	testutil.Require(t, err == nil, func() { t.Fatal(err) })
	{
		err := worker.waitForWindowDecisions(context.Background(), queue.Lease{}, 10)
		testutil.Require(t, err == nil, func() { t.Fatal(err) })
	}
	testutil.Require(t, output.Len() == 0, func() { t.Fatal("logged waiting when no decisions pending", output.String()) })
}
