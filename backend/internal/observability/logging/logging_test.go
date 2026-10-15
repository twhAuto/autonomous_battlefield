// Checks process logger creation and environment-configured log levels.

package logging

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
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

func TestInvalidLevelIsReported(t *testing.T) {
	var output bytes.Buffer
	newLogger(&output, "api", " verbose ")
	if !strings.Contains(output.String(), `"event":"log_level_invalid"`) || !strings.Contains(output.String(), `"value":"verbose"`) || !strings.Contains(output.String(), `"level":"WARN"`) || !strings.Contains(output.String(), `"fallback_level":"INFO"`) {
		t.Fatalf("invalid level not reported: %s", output.String())
	}
	output.Reset()
	newLogger(&output, "api", "debug")
	if output.Len() != 0 {
		t.Fatalf("valid level logged a warning: %s", output.String())
	}
}
