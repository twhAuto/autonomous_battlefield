package authn

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/twh09/autonomous_battlefield/backend/internal/apierror"
)

type Role string

const (
	Player      Role = "player"
	CI          Role = "ci"
	Developer   Role = "developer"
	Admin       Role = "admin"
	LegacyOwner      = "legacy-admin"
)

const permissionDeniedMessage = "permission denied"

var ErrForbidden = errors.New(permissionDeniedMessage)

// WithActor is for trusted authentication adapters and tests, never client input.
func WithActor(ctx context.Context, actor Actor) context.Context {
	if observe, ok := ctx.Value(actorObserverKey{}).(func(Actor)); ok {
		observe(actor)
	}
	return context.WithValue(ctx, actorContextKey{}, actor)
}

type actorObserverKey struct{}

// WithActorObserver lets outer request logging observe an identity established
// by inner authentication middleware without trusting client identity headers.
func WithActorObserver(ctx context.Context, observe func(Actor)) context.Context {
	return context.WithValue(ctx, actorObserverKey{}, observe)
}
func (a Actor) CanInspect() bool { return a.Role == Admin || a.Role == Developer }
func (a Actor) CanManage(owner string) bool {
	return a.Role != CI && a.ID != "" && (a.Role == Admin || a.ID == owner)
}
func Owner(ctx context.Context) string {
	a, ok := ActorFromContext(ctx)
	if !ok {
		return LegacyOwner
	}
	return a.ID
}
func IsPlayer(ctx context.Context) bool {
	a, ok := ActorFromContext(ctx)
	return ok && a.Role == Player
}
func AuthorizeOwner(ctx context.Context, owner string) error {
	a, ok := ActorFromContext(ctx)
	if !ok || !a.CanManage(owner) {
		return ErrForbidden
	}
	return nil
}

// BrowserProtection uses a configured origin, never a client-controlled Host header.
func BrowserProtection(origin string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin != "" && strings.HasPrefix(r.URL.Path, "/v1/") && r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" {
			if r.Header.Get("Origin") != origin {
				apierror.Write(w, r, http.StatusForbidden, "origin not permitted")
				return
			}
			if r.ContentLength != 0 && strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
				apierror.Write(w, r, http.StatusUnsupportedMediaType, "JSON required")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// Permit checks a registered route. An unassigned permission always denies access.
func Permit(permission string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, ok := ActorFromContext(r.Context())
		if !ok {
			apierror.Write(w, r, 401, "authentication required")
			return
		}
		allowed := (permission == "play" && (a.Role == Player || a.CanInspect())) || (permission == "inspect" && a.CanInspect()) || (permission == "admin" && a.Role == Admin)
		if !allowed {
			apierror.Write(w, r, 403, permissionDeniedMessage)
			return
		}
		next(w, r)
	}
}

// RoutePolicy is the complete public API permission registry. Unregistered routes deny.
func RoutePolicy(next http.Handler) http.Handler {
	mux := http.NewServeMux()
	for _, pattern := range []string{"GET /v1/factions", "GET /v1/faction-rulesets", "GET /v1/factions/{id}/name", "POST /v1/factions", "PUT /v1/factions/{id}", "DELETE /v1/factions/{id}", "GET /v1/scenarios", "GET /v1/model-profiles", "POST /v1/battles", "GET /v1/battles", "GET /v1/battles/{id}", "POST /v1/battles/{id}/stop", "POST /v1/battles/{id}/pause", "POST /v1/battles/{id}/resume", "GET /v1/battles/{id}/factions/{factionId}/view", "GET /v1/battles/{id}/events", "GET /v1/faction-background-generations/options", "POST /v1/faction-background-generations", "GET /v1/faction-background-generations/{id}"} {
		mux.HandleFunc(pattern, Permit("play", next.ServeHTTP))
	}
	for _, pattern := range []string{"GET /v1/battles/{id}/decisions", "GET /v1/battles/{id}/decisions/{requestId}/context"} {
		mux.HandleFunc(pattern, Permit("inspect", next.ServeHTTP))
	}
	for _, pattern := range []string{"GET /v1/admin/model-access", "PUT /v1/admin/model-access", "GET /v1/admin/members", "POST /v1/admin/members", "DELETE /v1/admin/members/{id}", "POST /v1/admin/members/retry"} {
		mux.HandleFunc(pattern, Permit("admin", next.ServeHTTP))
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { apierror.Write(w, r, 403, permissionDeniedMessage) })
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if actor, ok := ActorFromContext(r.Context()); ok && actor.Role == CI {
			if r.Method == http.MethodGet && (r.URL.Path == "/v1/auth/session" || r.URL.Path == "/v1/scenarios") {
				next.ServeHTTP(w, r)
			} else {
				apierror.Write(w, r, http.StatusForbidden, permissionDeniedMessage)
			}
			return
		}
		if r.URL.Path == "/v1/auth/login" || r.URL.Path == "/v1/auth/session" || r.URL.Path == "/v1/auth/logout" || !strings.HasPrefix(r.URL.Path, "/v1/") {
			next.ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
}
