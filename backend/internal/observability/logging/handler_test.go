// Checks readable event messages preserve structured attributes and correlate the current
// trace without leaking across calls.

package logging

import (
	"fmt"
	"net"

	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"testing/slogtest"
	"time"

	"go.opentelemetry.io/otel/trace"
)

func decodeLogs(t *testing.T, data []byte) []map[string]any {
	t.Helper()
	var rows []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		var row map[string]any
		{
			err := json.Unmarshal(line, &row)
			if err != nil {
				t.Fatal(err)
			}
		}
		rows = append(rows, row)
	}
	return rows
}

func TestHandlerPreservesStructuredLogging(t *testing.T) {
	var output bytes.Buffer
	handler := NewHandler(slog.NewJSONHandler(&output, nil))
	{
		err := slogtest.TestHandler(handler, func() []map[string]any { return decodeLogs(t, output.Bytes()) })
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestReadableMessagesPreserveEventsAndFields(t *testing.T) {
	cases := []struct {
		event string
		attrs []any
		want  []string
	}{
		{"database_connect_retry", []any{"attempt", 3, "retry_in", time.Second, "error", errors.New("connection refused")}, []string{"Database connection failed; retrying", "attempt: 3", "retry in: 1s", "error: connection refused"}},
		{"battle_claimed", []any{"battle_id", "castle-42", "attempt", 1}, []string{"Battle claimed; starting simulation", "battle: castle-42", "worker: engine-1", "attempt: 1"}},
		{"battle_progress", []any{"battle_id", "castle-42", "current_tick", 120, "requested_ticks", 600, "elapsed_ms", 94000}, []string{"Battle simulation progressing", "tick: 120", "requested ticks: 600", "elapsed (ms): 94000"}},
		{"decision_window_waiting", []any{"target_tick", 120, "pending_count", 2}, []string{"Waiting for faction decisions", "pending factions: 2", "target tick: 120"}},
		{"decision_resolved", []any{"faction_id", "blue", "decision_status", "fallback", "error_category", "timeout", "fallback_source", "previous_plan"}, []string{"faction: blue", "decision status: fallback", "cause: timeout", "fallback: previous_plan"}},
		{"battle_completed", []any{"ticks_run", 600, "winner_faction_id", nil}, []string{"Battle completed", "ticks completed: 600"}},
		{"new_operational_event", []any{"reason", "", "prompt", "PRIVATE-PROMPT", "observation", "PRIVATE-OBSERVATION"}, []string{"New operational event"}},
	}
	for _, tc := range cases {
		t.Run(tc.event, func(t *testing.T) {
			var output bytes.Buffer
			logger := slog.New(NewHandler(slog.NewJSONHandler(&output, nil))).With("service", "engine", "worker_id", "engine-1")
			logger.Info(tc.event, tc.attrs...)
			row := decodeLogs(t, output.Bytes())[0]
			if row["event"] != tc.event || row["service"] != "engine" {
				t.Fatal(row)
			}
			checkReadableMessage(t, row["msg"].(string), append(tc.want, "[engine]"))
			if _, ok := row["trace_id"]; ok {
				t.Fatal("invented trace ID")
			}
		})
	}
}

// checkReadableMessage requires every wanted fragment and no private fields, nil
// values or empty winner in a readable log message.
func checkReadableMessage(t *testing.T, msg string, want []string) {
	t.Helper()
	for _, fragment := range want {
		if !strings.Contains(msg, fragment) {
			t.Fatalf("%q missing %q", msg, fragment)
		}
	}
	for _, forbidden := range []string{"PRIVATE-", "<nil>", "winner:"} {
		if strings.Contains(msg, forbidden) {
			t.Fatal(msg)
		}
	}
}

func TestHandlerCorrelatesActiveSpanWithoutLeakingAcrossCalls(t *testing.T) {
	span := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}, TraceFlags: trace.FlagsSampled})
	ctx := trace.ContextWithSpanContext(context.Background(), span)
	var output bytes.Buffer
	logger := slog.New(NewHandler(slog.NewJSONHandler(&output, nil))).With("service", "api")
	logger.WithGroup("details").With("status", 200).InfoContext(ctx, "http_request_completed", "path", "/v1/battles")
	logger.Info("database_connected")
	rows := decodeLogs(t, output.Bytes())
	if rows[0]["trace_id"] != span.TraceID().String() || rows[0]["span_id"] != span.SpanID().String() || rows[0]["event"] != "http_request_completed" {
		t.Fatal(rows[0])
	}
	if rows[0]["details"].(map[string]any)["status"] != float64(200) {
		t.Fatal(rows[0])
	}
	{
		_, ok := rows[1]["trace_id"]
		if ok {
			t.Fatal("trace leaked into unrelated log", rows[1])
		}
	}
}

func TestHandlerPreservesFreeTextAndSiblingAttributes(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(NewHandler(slog.NewJSONHandler(&output, nil)))
	a := logger.With("battle_id", "a")
	b := logger.With("battle_id", "b")
	a.Info("battle_paused")
	b.Info("battle_paused")
	logger.Info("already readable_error text")
	rows := decodeLogs(t, output.Bytes())
	if !strings.Contains(rows[0]["msg"].(string), "battle: a") || !strings.Contains(rows[1]["msg"].(string), "battle: b") {
		t.Fatal(rows)
	}
	if rows[2]["msg"] != "already readable_error text" || rows[2]["event"] != nil {
		t.Fatal(rows[2])
	}
}

type countingValue struct{ calls *int }

func (v countingValue) LogValue() slog.Value { *v.calls++; return slog.IntValue(*v.calls) }

func TestHandlerResolvesValuesOnce(t *testing.T) {
	calls := 0
	var output bytes.Buffer
	logger := slog.New(NewHandler(slog.NewJSONHandler(&output, nil))).With(slog.Group("", "attempt", countingValue{&calls}))
	logger.Info("database_connect_retry", "status", countingValue{&calls})
	logger.Info("database_connect_retry")
	rows := decodeLogs(t, output.Bytes())
	if calls != 2 || rows[0]["attempt"] != float64(1) || rows[0]["status"] != float64(2) || rows[1]["attempt"] != float64(1) {
		t.Fatal("values evaluated more than once", calls, rows)
	}
	if !strings.Contains(rows[0]["msg"].(string), "attempt: 1") || !strings.Contains(rows[0]["msg"].(string), "HTTP status: 2") {
		t.Fatal("summary and fields disagree", rows[0])
	}
}

func TestHandlerRedactsErrorValuesUnderAnyKey(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(NewHandler(slog.NewJSONHandler(&output, nil)))
	dial := &net.OpError{Op: "dial", Net: "tcp", Addr: &net.TCPAddr{IP: net.ParseIP("10.0.0.5"), Port: 5432}, Err: errors.New("connect: connection refused")}
	logger.With("cause", dial).Error("database_connect_retry", "error", fmt.Errorf("ping: %w", dial), slog.Group("detail", "inner", dial), "plain", "kept")
	got := output.String()
	if strings.Contains(got, "10.0.0.5") {
		t.Fatalf("log leaked the address: %s", got)
	}
	for _, want := range []string{`"error":"ping: dial tcp: connect: connection refused"`, `"cause":"dial tcp: connect: connection refused"`, `"inner":"dial tcp: connect: connection refused"`, `"plain":"kept"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("log %s lacks %s", got, want)
		}
	}
}

func TestHandlerLogsTypedNilErrorsWithoutPanicking(t *testing.T) {
	var output bytes.Buffer
	var operation *net.OpError
	slog.New(NewHandler(slog.NewJSONHandler(&output, nil))).Error("database_connect_retry", "error", operation)
	if !strings.Contains(output.String(), `"error":"<nil>"`) {
		t.Fatalf("typed-nil error logged as %s", output.String())
	}
}
