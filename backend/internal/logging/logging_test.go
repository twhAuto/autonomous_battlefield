package logging

import (
	"context"
	"log/slog"
	"testing"
)

func TestNewReturnsEnabledStructuredLogger(t *testing.T) {
	logger := New("api")
	if logger == nil {
		t.Fatal("New() returned nil")
	}
	if !logger.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("logger is not enabled at info level")
	}
	if logger.Enabled(context.Background(), slog.LevelDebug) {
		t.Fatal("logger unexpectedly enabled debug level")
	}
}
