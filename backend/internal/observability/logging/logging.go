// Package logging configures process-wide structured operational logs.
package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// New returns a JSON logger suitable for container stdout collection. The
// service attribute lets logs from API and engine replicas share one sink.
func New(service string) *slog.Logger {
	return newLogger(os.Stdout, service, os.Getenv("LOG_LEVEL"))
}

// newLogger falls back to INFO for an unrecognised level and says so, rather than
// silently ignoring a typo such as "verbose".
func newLogger(output io.Writer, service, rawLevel string) *slog.Logger {
	level := slog.LevelInfo
	raw := strings.TrimSpace(rawLevel)
	invalid := raw != "" && level.UnmarshalText([]byte(raw)) != nil
	if invalid {
		level = slog.LevelInfo
	}
	handler := slog.NewJSONHandler(output, &slog.HandlerOptions{Level: level})
	logger := slog.New(NewHandler(handler)).With("service", service)
	if invalid {
		logger.Warn("log_level_invalid", "value", raw, "fallback_level", level.String())
	}
	return logger
}
