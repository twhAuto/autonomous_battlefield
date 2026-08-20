// Package logging configures process-wide structured operational logs.
package logging

import (
	"log/slog"
	"os"
)

// New returns a JSON logger suitable for container stdout collection. The
// service attribute lets logs from API and engine replicas share one sink.
func New(service string) *slog.Logger {
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	return slog.New(handler).With("service", service)
}
