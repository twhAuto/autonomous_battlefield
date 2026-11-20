// Serves process probes and metrics across startup, activation, and shutdown, delegating
// application traffic only when active.

package httpapi

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/apierror"
)

// LifecycleHandler exposes process lifecycle probes before the application is
// initialized, then delegates application traffic once its dependencies are
// ready. Liveness deliberately does not depend on PostgreSQL: restarting a
// healthy process cannot repair an unavailable dependency.
type LifecycleHandler struct {
	mu        sync.RWMutex
	handler   http.Handler
	pinger    Pinger
	ready     func() bool
	metrics   http.Handler
	started   atomic.Bool
	accepting atomic.Bool
	timeout   time.Duration
}

// NewLifecycleHandler serves the process probes; a nil metrics handler answers
// /metrics with 404.
func NewLifecycleHandler(metrics http.Handler) *LifecycleHandler {
	return &LifecycleHandler{timeout: readinessTimeout, metrics: metrics}
}

// Activate marks startup complete and enables application traffic. The ready
// callback may be nil when dependency health alone determines readiness.
func (h *LifecycleHandler) Activate(handler http.Handler, pinger Pinger, ready func() bool) {
	h.mu.Lock()
	h.handler = handler
	h.pinger = pinger
	h.ready = ready
	h.mu.Unlock()
	h.started.Store(true)
	h.accepting.Store(true)
}

// BeginShutdown makes readiness and application traffic fail before the HTTP
// server begins draining in-flight requests.
func (h *LifecycleHandler) BeginShutdown() {
	h.accepting.Store(false)
}

func (h *LifecycleHandler) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	switch request.URL.Path {
	case "/startupz":
		if !h.started.Load() {
			apierror.Write(w, request, http.StatusServiceUnavailable, "starting")
			return
		}
		writeOK(w)
	case "/livez", "/healthz":
		writeOK(w)
	case "/readyz":
		h.serveReadiness(w, request)
	case "/metrics":
		if h.metrics == nil {
			http.NotFound(w, request)
			return
		}
		h.metrics.ServeHTTP(w, request)
	default:
		h.serveApplication(w, request)
	}
}

func (h *LifecycleHandler) serveReadiness(w http.ResponseWriter, request *http.Request) {
	if !h.accepting.Load() {
		apierror.Write(w, request, http.StatusServiceUnavailable, "not ready")
		return
	}

	h.mu.RLock()
	pinger := h.pinger
	ready := h.ready
	h.mu.RUnlock()
	if pinger == nil || (ready != nil && !ready()) {
		apierror.Write(w, request, http.StatusServiceUnavailable, "not ready")
		return
	}

	ctx, cancel := context.WithTimeout(request.Context(), h.timeout)
	defer cancel()
	if err := pinger.Ping(ctx); err != nil {
		apierror.Write(w, request, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeOK(w)
}

func (h *LifecycleHandler) serveApplication(w http.ResponseWriter, request *http.Request) {
	if !h.accepting.Load() {
		apierror.Write(w, request, http.StatusServiceUnavailable, "service unavailable")
		return
	}
	h.mu.RLock()
	handler := h.handler
	h.mu.RUnlock()
	if handler == nil {
		apierror.Write(w, request, http.StatusServiceUnavailable, "service unavailable")
		return
	}
	handler.ServeHTTP(w, request)
}

func writeOK(w http.ResponseWriter) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}
