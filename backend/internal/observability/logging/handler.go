// Enriches structured log events with readable messages and active trace IDs while
// preserving their original fields, and redacts connection identities from errors.

package logging

import (
	"context"
	"log/slog"
	"slices"
	"strings"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/observability/redact"
	"go.opentelemetry.io/otel/trace"
)

// NewHandler decorates structured application events with readable messages and
// the active span. Attribute groups retain their normal slog semantics.
func NewHandler(sink slog.Handler) slog.Handler { return &eventHandler{sink: sink} }

type binding struct {
	group string
	attrs []slog.Attr
}

type eventHandler struct {
	sink     slog.Handler
	bindings []binding
}

func (h *eventHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.sink.Enabled(ctx, level)
}

func (h *eventHandler) Handle(ctx context.Context, record slog.Record) error {
	// Resolve once so the human summary and structured fields agree even when
	// a LogValuer reads changing state.
	resolvedRecord := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	record.Attrs(func(a slog.Attr) bool { resolvedRecord.AddAttrs(resolveAttr(a)); return true })
	record = resolvedRecord
	fields := make(map[string]slog.Value)
	grouped := false
	for _, item := range h.bindings {
		if item.group != "" {
			grouped = true
		}
		if !grouped {
			collectFields(fields, item.attrs)
		}
	}
	if !grouped {
		record.Attrs(func(a slog.Attr) bool { collectFields(fields, []slog.Attr{a}); return true })
	}
	metadata := []slog.Attr{}
	if isEvent(record.Message) {
		metadata = append(metadata, slog.String("event", record.Message))
		record.Message = readableMessage(record.Message, fields)
	}
	span := trace.SpanContextFromContext(ctx)
	if span.IsValid() {
		metadata = append(metadata, slog.String("trace_id", span.TraceID().String()), slog.String("span_id", span.SpanID().String()))
	}
	// Bind metadata before user groups so correlation fields stay at the root.
	sink := h.sink.WithAttrs(metadata)
	for _, item := range h.bindings {
		if item.group != "" {
			sink = sink.WithGroup(item.group)
		} else {
			sink = sink.WithAttrs(item.attrs)
		}
	}
	return sink.Handle(ctx, record)
}

func (h *eventHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	resolved := slices.Clone(attrs)
	for i := range resolved {
		resolved[i] = resolveAttr(resolved[i])
	}
	return &eventHandler{sink: h.sink, bindings: append(slices.Clone(h.bindings), binding{attrs: resolved})}
}

func (h *eventHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return &eventHandler{sink: h.sink, bindings: append(slices.Clone(h.bindings), binding{group: name})}
}

func collectFields(fields map[string]slog.Value, attrs []slog.Attr) {
	for _, a := range attrs {
		value := a.Value.Resolve()
		if a.Key == "" && value.Kind() == slog.KindGroup {
			collectFields(fields, value.Group())
		} else if value.Kind() != slog.KindGroup {
			fields[a.Key] = value
		}
	}
}

func isEvent(message string) bool {
	if !strings.Contains(message, "_") {
		return false
	}
	for _, c := range message {
		if c != '_' && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// resolveAttr also renders error values through redact, so no log line, whatever its
// key, carries the database user, host or address a connection error prints.
func resolveAttr(a slog.Attr) slog.Attr {
	a.Value = a.Value.Resolve()
	if err, ok := a.Value.Any().(error); ok && a.Value.Kind() == slog.KindAny {
		a.Value = slog.StringValue(redact.Error(err))
	}
	if a.Value.Kind() == slog.KindGroup {
		attrs := slices.Clone(a.Value.Group())
		for i := range attrs {
			attrs[i] = resolveAttr(attrs[i])
		}
		a.Value = slog.GroupValue(attrs...)
	}
	return a
}
