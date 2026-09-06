// Package logging configures process-wide structured operational logs.
package logging

import (
	"log/slog"
	"os"
	"strings"
)

// New returns a JSON logger suitable for container stdout collection. The
// service attribute lets logs from API and engine replicas share one sink.
func New(service string) *slog.Logger {
	level := slog.LevelInfo
	raw := strings.TrimSpace(os.Getenv("LOG_LEVEL"))
	if raw != "" {
		if err := level.UnmarshalText([]byte(raw)); err != nil {
			level = slog.LevelInfo
		}
	}
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	return slog.New(NewHandler(handler)).With("service", service)
}
