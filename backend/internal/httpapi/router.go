// Composes business route registrars and middleware beneath request logging; process
// probes are handled separately.

package httpapi

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/apierror"
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

	for _, registrar := range registrars {
		registrar.RegisterRoutes(mux)
	}

	var handler http.Handler = unmatchedAPIRoutes(mux)
	for index := len(middlewares) - 1; index >= 0; index-- {
		handler = middlewares[index](handler)
	}
	return requestLogging(handler, mux, slog.Default())
}

// unmatchedAPIRoutes gives unknown /v1 routes the JSON error envelope. ServeMux would
// otherwise answer them with its own plain-text 404 or 405. The mux still decides which
// status applies and computes the Allow header, so routing semantics are unchanged;
// only the body format is replaced.
func unmatchedAPIRoutes(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallback, pattern := mux.Handler(r)
		if pattern != "" || !strings.HasPrefix(r.URL.Path, "/v1/") {
			mux.ServeHTTP(w, r)
			return
		}

		probe := &statusProbe{header: http.Header{}}
		fallback.ServeHTTP(probe, r)
		if probe.status < http.StatusBadRequest {
			// A redirect or other non-error outcome: let the mux answer it directly.
			mux.ServeHTTP(w, r)
			return
		}
		if allow := probe.header.Get("Allow"); allow != "" {
			w.Header().Set("Allow", allow)
		}
		message := "route not found"
		if probe.status == http.StatusMethodNotAllowed {
			message = "method not allowed"
		}
		apierror.Write(w, r, probe.status, message)
	})
}

// statusProbe records what the mux's fallback would send without writing it anywhere.
type statusProbe struct {
	header http.Header
	status int
}

func (p *statusProbe) Header() http.Header { return p.header }

func (p *statusProbe) WriteHeader(status int) {
	if p.status == 0 {
		p.status = status
	}
}

func (p *statusProbe) Write(body []byte) (int, error) {
	if p.status == 0 {
		p.status = http.StatusOK
	}
	return len(body), nil
}
