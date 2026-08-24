package httpapi

import (
	"context"
	"net/http"
	"time"
)

const readinessTimeout = time.Second

type Pinger interface {
	Ping(context.Context) error
}

type ReadinessRoutes struct {
	pinger  Pinger
	timeout time.Duration
}

func NewReadinessRoutes(pinger Pinger) *ReadinessRoutes {
	return &ReadinessRoutes{pinger: pinger, timeout: readinessTimeout}
}

func (r *ReadinessRoutes) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /readyz", r.ready)
}

func (r *ReadinessRoutes) ready(w http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), r.timeout)
	defer cancel()

	if err := r.pinger.Ping(ctx); err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}
