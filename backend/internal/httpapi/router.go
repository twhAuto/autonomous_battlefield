package httpapi

import (
	"log/slog"
	"net/http"
)

type RouteRegistrar interface {
	RegisterRoutes(*http.ServeMux)
}

func NewRouter(registrars ...RouteRegistrar) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", health)
	for _, registrar := range registrars {
		registrar.RegisterRoutes(mux)
	}

	return requestLogging(mux, slog.Default())
}

func health(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}
