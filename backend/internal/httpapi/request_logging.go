// Wraps HTTP requests with correlation IDs, response status and timing logs, and
// route-pattern metrics.

package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"regexp"
	"sync/atomic"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	"github.com/twh09/autonomous_battlefield/backend/internal/requestid"
	"github.com/twh09/autonomous_battlefield/backend/internal/telemetry"
)

// clientRequestID limits caller-supplied correlation IDs to characters that are safe to
// echo and log; anything else is replaced with a generated ID.
var clientRequestID = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

func requestLogging(next http.Handler, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		requestID := acceptedRequestID(r.Header.Get("X-Request-ID"))
		w.Header().Set("X-Request-ID", requestID)
		r = r.WithContext(requestid.With(r.Context(), requestID))
		var authFailure atomic.Pointer[string]
		r = r.WithContext(authn.WithAuthFailureObserver(r.Context(), func(reason string) { authFailure.Store(&reason) }))
		var authenticated atomic.Pointer[authn.Actor]
		r = r.WithContext(authn.WithActorObserver(r.Context(), func(actor authn.Actor) {
			authenticated.Store(&actor)
		}))
		response := &loggingResponseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(response, r)
		duration := time.Since(started)
		telemetry.ObserveHTTPRequest(r.Method, r.Pattern, response.status, duration)

		// Probes never reach this middleware: LifecycleHandler answers them first.
		level := slog.LevelInfo
		if response.status >= http.StatusInternalServerError {
			level = slog.LevelError
		} else if response.status >= http.StatusBadRequest {
			level = slog.LevelWarn
		}
		actor := authn.Actor{}
		if verified := authenticated.Load(); verified != nil {
			actor = *verified
		}
		reason := ""
		if failure := authFailure.Load(); failure != nil {
			reason = *failure
		}
		logger.Log(r.Context(), level, "http_request_completed",
			"auth_failure", reason,
			"actor_id", actor.ID,
			"actor_role", actor.Role,
			"request_id", requestID,
			"method", r.Method,
			"path", r.URL.Path,
			"status", response.status,
			"response_bytes", response.bytes,
			"duration", duration,
			"duration_ms", duration.Milliseconds(),
		)
	})
}

type loggingResponseWriter struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
}

func (w *loggingResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status = status
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *loggingResponseWriter) Write(payload []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	written, err := w.ResponseWriter.Write(payload)
	w.bytes += written
	return written, err
}

// Unwrap allows http.ResponseController to reach optional interfaces exposed
// by the original writer.
func (w *loggingResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func acceptedRequestID(candidate string) string {
	if clientRequestID.MatchString(candidate) {
		return candidate
	}
	// crypto/rand.Read never fails (Go 1.24+).
	var bytes [16]byte
	_, _ = rand.Read(bytes[:])
	return hex.EncodeToString(bytes[:])
}

// RequestID reports the correlation identifier the logging middleware attached.
func RequestID(ctx context.Context) string {
	return requestid.From(ctx)
}
