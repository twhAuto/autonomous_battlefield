// Checks database startup retries log each failure and stop on cancellation or invalid
// configuration.

package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestPostgresRetryLogsAttemptsAndStopsWhenContextIsCancelled(t *testing.T) {
	logs := captureLogs(t)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()

	_, err := ConnectWithRetry(
		ctx,
		"postgres://battlefield:battlefield@127.0.0.1:1/battlefield?sslmode=disable",
		Options{MaxConnections: 1},
		10*time.Millisecond,
		5*time.Millisecond,
	)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context deadline exceeded", err)
	}
	line, _, _ := strings.Cut(logs.String(), "\n")
	var record struct {
		Level, Msg, Error string
		Attempt           int
		RetryIn           time.Duration `json:"retry_in"`
	}
	if err := json.Unmarshal([]byte(line), &record); err != nil {
		t.Fatalf("no retry warning logged: %q", logs.String())
	}
	if record.Level != "WARN" || record.Msg != "database_connect_retry" || record.Attempt != 1 || record.RetryIn != 5*time.Millisecond || record.Error == "" {
		t.Fatalf("first retry log = %+v", record)
	}
}

func TestPostgresRetryRejectsInvalidConfigurationWithoutRetrying(t *testing.T) {
	logs := captureLogs(t)
	for _, test := range []struct {
		name           string
		url            string
		attemptTimeout time.Duration
		retryInterval  time.Duration
	}{
		{name: "invalid URL", url: "://invalid", attemptTimeout: time.Second, retryInterval: time.Second},
		{name: "no attempt timeout", url: "postgres://127.0.0.1:1/db", retryInterval: time.Second},
		{name: "no retry interval", url: "postgres://127.0.0.1:1/db", attemptTimeout: time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ConnectWithRetry(context.Background(), test.url, Options{MaxConnections: 1}, test.attemptTimeout, test.retryInterval); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
	if logs.Len() != 0 {
		t.Fatalf("invalid configuration was retried: %q", logs.String())
	}
}

// captureLogs sends the default logger's JSON output to a buffer for the test.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logs bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(original) })
	return &logs
}
