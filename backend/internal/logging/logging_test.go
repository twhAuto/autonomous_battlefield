package logging

import (
	"context"
	"log/slog"
	"testing"
)

func TestNewReturnsEnabledStructuredLogger(t *testing.T) {
	t.Setenv("LOG_LEVEL", "")
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

func TestConfiguredLevel(t *testing.T) {
	for _, tc := range []struct {
		raw         string
		debug, info bool
	}{
		{"", false, true}, {"debug", true, true}, {" WARN ", false, false}, {"invalid", false, true},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			t.Setenv("LOG_LEVEL", tc.raw)
			logger := New("test")
			if logger.Enabled(context.Background(), slog.LevelDebug) != tc.debug || logger.Enabled(context.Background(), slog.LevelInfo) != tc.info {
				t.Fatal("incorrect level", tc.raw)
			}
		})
	}
}
