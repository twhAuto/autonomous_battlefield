// Maps stable operational event names to readable log messages using structured fields
// supplied by callers.

package logging

import (
	"fmt"
	"log/slog"
	"strings"
)

// Only operational fields are included in the compact message. Arbitrary
// attributes, observations, prompts and provider payloads are never copied.
var messageFields = []struct{ key, label string }{
	{"request_id", "request"}, {"battle_id", "battle"}, {"faction_id", "faction"}, {"worker_id", "worker"},
	{"attempt", "attempt"}, {"scenario_id", "scenario"}, {"requested_ticks", "requested ticks"},
	{"current_tick", "tick"}, {"target_tick", "target tick"}, {"ticks_run", "ticks completed"},
	{"pending_count", "pending factions"}, {"participant_count", "factions"},
	{"accepted_count", "accepted plans"}, {"fallback_count", "fallback plans"},
	{"decision_status", "decision status"}, {"fallback_source", "fallback"},
	{"error_category", "cause"}, {"error_code", "error code"},
	{"provider", "provider"}, {"runtime", "runtime"}, {"profile_id", "profile"},
	{"duration_ms", "duration (ms)"}, {"elapsed_ms", "elapsed (ms)"},
	{"decision_timeout_ms", "timeout (ms)"}, {"remaining_ms", "remaining (ms)"},
	{"retry_in", "retry in"}, {"input_tokens", "input tokens"}, {"output_tokens", "output tokens"},
	{"method", "method"}, {"path", "path"}, {"status", "HTTP status"},
	{"address", "address"}, {"winner_faction_id", "winner"}, {"reason", "reason"},
	{"accepted", "accepted"}, {"field", "field"}, {"code", "code"}, {"missing_fields", "missing fields"},
	{"error", "error"},
}

var eventTitles = map[string]string{
	"database_connect_retry":       "Database connection failed; retrying",
	"database_connected":           "Database connected",
	"battle_worker_ready":          "Battle worker ready to claim queued battles",
	"battle_claimed":               "Battle claimed; starting simulation",
	"battle_progress":              "Battle simulation progressing",
	"battle_tick_completed":        "Battle tick persisted",
	"battle_completed":             "Battle completed",
	"battle_processing_stopped":    "Battle processing stopped after an error",
	"battle_lease_lost":            "Battle ownership lost; worker stopped processing",
	"battle_requeued_for_shutdown": "Battle requeued for another worker during shutdown",
	"decision_window_started":      "Preparing faction decisions",
	"decision_window_waiting":      "Waiting for faction decisions",
	"decision_window_completed":    "Faction decisions resolved; resuming simulation",
	"decision_runner_started":      "AI decision runner started",
	"decision_runner_completed":    "AI decision runner completed",
	"decision_runner_failed":       "AI decision runner failed",
	"decision_resolved":            "Faction decision resolved",
	"decision_plan_submitted":      "Faction plan submission processed",
	"http_request_completed":       "HTTP request completed",
}

func readableMessage(event string, fields map[string]slog.Value) string {
	title := eventTitles[event]
	if title == "" {
		title = strings.ReplaceAll(event, "_", " ")
		title = strings.ToUpper(title[:1]) + title[1:]
	}
	parts := []string{title}
	for _, field := range messageFields {
		value, ok := fields[field.key]
		if !ok || value.Any() == nil || value.String() == "" {
			continue
		}
		parts = append(parts, field.label+": "+fmt.Sprint(value.Any()))
	}
	message := strings.Join(parts, "; ")
	if service, ok := fields["service"]; ok {
		message = "[" + service.String() + "] " + message
	}
	return message
}
