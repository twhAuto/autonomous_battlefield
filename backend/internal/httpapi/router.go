package httpapi

import (
	"log/slog"
	"net/http"
)

type RouteRegistrar interface {
	RegisterRoutes(*http.ServeMux)
}

type Middleware func(http.Handler) http.Handler

func NewRouter(registrars ...RouteRegistrar) http.Handler {
	return NewRouterWithMiddleware(nil, registrars...)
}

func NewRouterWithMiddleware(middlewares []Middleware, registrars ...RouteRegistrar) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", health)
	mux.HandleFunc("GET /livez", health)
	mux.HandleFunc("GET /startupz", health)
	for _, registrar := range registrars {
		registrar.RegisterRoutes(mux)
	}

	var handler http.Handler = mux
	for index := len(middlewares) - 1; index >= 0; index-- {
		handler = middlewares[index](handler)
	}
	return requestLogging(handler, slog.Default())
}

func health(w http.ResponseWriter, _ *http.Request) {
	writeOK(w)
}
