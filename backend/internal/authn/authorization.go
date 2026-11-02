package authn

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/twh09/autonomous_battlefield/backend/internal/apierror"
)

type Role string

const (
	Player      Role = "player"
	CI          Role = "ci"
	Tester      Role = "tester"
	Admin       Role = "admin"
	LegacyOwner      = "legacy-admin"
	// Guest is anyone signed in through the guest Access application: read-only and
	// never mapped from an email.
	Guest Role = "guest"
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
func (a Actor) CanInspect() bool { return a.Role == Admin || a.Role == Tester }

// CanView reports a signed-in person, Guests included; service identities read nothing.
func (a Actor) CanView() bool { return a.Role == Guest || a.Role == Player || a.CanInspect() }
func (a Actor) CanManage(owner string) bool {
	return a.Role != CI && a.Role != Guest && a.ID != "" && (a.Role == Admin || a.ID == owner)
}
func Owner(ctx context.Context) string {
	a, ok := ActorFromContext(ctx)
	if !ok {
		return LegacyOwner
	}
	return a.ID
}

// GameplayOnly reports a Player or Guest, who receive redacted gameplay responses.
func GameplayOnly(ctx context.Context) bool {
	a, ok := ActorFromContext(ctx)
	return ok && (a.Role == Player || a.Role == Guest)
}
func AuthorizeOwner(ctx context.Context, owner string) error {
	a, ok := ActorFromContext(ctx)
	if !ok || !a.CanManage(owner) {
		return ErrForbidden
	}
	return nil
}

// BrowserProtection uses configured origins, never a client-controlled Host header. Each
// configured origin, such as the guest hostname, is accepted; requests from it still
// carry only that hostname's Access identity. No origin disables the check.
func BrowserProtection(origins []string, next http.Handler) http.Handler {
	// An unset origin must never match a request that sends no Origin header.
	origins = slices.DeleteFunc(slices.Clone(origins), func(origin string) bool { return origin == "" })
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(origins) > 0 && strings.HasPrefix(r.URL.Path, "/v1/") && r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" {
			if !slices.Contains(origins, r.Header.Get("Origin")) {
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

// Permission is the access level a route requires.
type Permission string

const (
	// PermissionView admits guests and anyone who can play.
	PermissionView Permission = "view"
	// PermissionPlay admits players and anyone who can inspect.
	PermissionPlay Permission = "play"
	// PermissionInspect admits testers and administrators.
	PermissionInspect Permission = "inspect"
	// PermissionAdmin admits administrators only.
	PermissionAdmin Permission = "admin"
)

// permits reports whether a holds permission. Each level admits every role above it.
func permits(permission Permission, a Actor) bool {
	switch permission {
	case PermissionView:
		return a.CanView()
	case PermissionPlay:
		return a.Role == Player || a.CanInspect()
	case PermissionInspect:
		return a.CanInspect()
	case PermissionAdmin:
		return a.Role == Admin
	default:
		return false
	}
}

// Permit checks a registered route. An unassigned permission always denies access.
func Permit(permission Permission, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, ok := ActorFromContext(r.Context())
		if !ok {
			apierror.Write(w, r, 401, "authentication required")
			return
		}
		if !permits(permission, a) {
			apierror.Write(w, r, 403, permissionDeniedMessage)
			return
		}
		next(w, r)
	}
}

// routeRule is one public API route and every access decision made about it.
type routeRule struct {
	// pattern is "METHOD /path" as registered on the router.
	pattern string
	// permission is required by RoutePolicy. Identity routes leave it empty: they
	// establish or describe identity, so RoutePolicy passes their paths through.
	permission Permission
	// public routes are served without authentication.
	public bool
	// ci routes are the only ones the CI service identity may call. CI matches the
	// exact method and path, so these patterns must not contain wildcards.
	ci bool
}

// routeRules is the complete public API access policy. RoutePolicy denies any /v1
// route not listed here, RequireAuthentication admits only the public ones without
// an identity, and the OpenAPI contract test checks the published specification
// against it, so the policy cannot drift between those places.
var routeRules = []routeRule{
	{pattern: "GET /v1/auth/session", public: true, ci: true},
	{pattern: "POST /v1/auth/login", public: true},
	{pattern: "POST /v1/auth/logout"},

	{pattern: "GET /v1/factions", permission: PermissionView},
	{pattern: "GET /v1/faction-rulesets", permission: PermissionView},
	{pattern: "POST /v1/factions", permission: PermissionPlay},
	{pattern: "PUT /v1/factions/{id}", permission: PermissionPlay},
	{pattern: "DELETE /v1/factions/{id}", permission: PermissionPlay},
	{pattern: "GET /v1/scenarios", permission: PermissionView, ci: true},
	{pattern: "GET /v1/model-profiles", permission: PermissionPlay},
	{pattern: "POST /v1/battles", permission: PermissionPlay},
	{pattern: "GET /v1/battles/allowance", permission: PermissionPlay},
	{pattern: "GET /v1/battles", permission: PermissionView},
	{pattern: "GET /v1/battles/{id}", permission: PermissionView},
	{pattern: "POST /v1/battles/{id}/stop", permission: PermissionPlay},
	{pattern: "POST /v1/battles/{id}/pause", permission: PermissionPlay},
	{pattern: "POST /v1/battles/{id}/resume", permission: PermissionPlay},
	{pattern: "GET /v1/battles/{id}/factions/{factionId}/view", permission: PermissionView},
	{pattern: "GET /v1/battles/{id}/events", permission: PermissionView},
	{pattern: "GET /v1/battles/{id}/replay", permission: PermissionView},
	{pattern: "GET /v1/battles/{id}/decisions", permission: PermissionView},
	{pattern: "GET /v1/faction-background-generations/options", permission: PermissionPlay},
	{pattern: "POST /v1/faction-background-generations", permission: PermissionPlay},
	{pattern: "GET /v1/faction-background-generations/{id}", permission: PermissionPlay},

	{pattern: "GET /v1/battles/{id}/decisions/{requestId}/context", permission: PermissionInspect},

	{pattern: "GET /v1/leagues", permission: PermissionView},
	{pattern: "GET /v1/leagues/{id}", permission: PermissionView},
	{pattern: "GET /v1/analytics", permission: PermissionView},
	{pattern: "POST /v1/leagues", permission: PermissionAdmin},
	{pattern: "POST /v1/leagues/{id}/cancel", permission: PermissionAdmin},
	{pattern: "POST /v1/leagues/{id}/entrants/{entrant}/disqualify", permission: PermissionAdmin},

	{pattern: "GET /v1/admin/model-access", permission: PermissionAdmin},
	{pattern: "PUT /v1/admin/model-access", permission: PermissionAdmin},
	{pattern: "GET /v1/admin/members", permission: PermissionAdmin},
	{pattern: "POST /v1/admin/members", permission: PermissionAdmin},
	{pattern: "DELETE /v1/admin/members/{id}", permission: PermissionAdmin},
	{pattern: "POST /v1/admin/members/retry", permission: PermissionAdmin},
}

// PublicAPIRoutes lists every "METHOD /path" pattern the public API serves.
func PublicAPIRoutes() []string {
	routes := make([]string, 0, len(routeRules))
	for _, rule := range routeRules {
		routes = append(routes, rule.pattern)
	}
	return routes
}

// rulePaths returns the paths of the rules selected by keep. Identity and public
// routes are matched by path for every method, so another method on the same path
// reaches the handler and receives its method-not-allowed response.
func rulePaths(keep func(routeRule) bool) map[string]bool {
	paths := map[string]bool{}
	for _, rule := range routeRules {
		if keep(rule) {
			_, path, _ := strings.Cut(rule.pattern, " ")
			paths[path] = true
		}
	}
	return paths
}

func rulePatterns(keep func(routeRule) bool) map[string]bool {
	patterns := map[string]bool{}
	for _, rule := range routeRules {
		if keep(rule) {
			patterns[rule.pattern] = true
		}
	}
	return patterns
}

var (
	publicPaths   = rulePaths(func(rule routeRule) bool { return rule.public })
	identityPaths = rulePaths(func(rule routeRule) bool { return rule.permission == "" })
	ciPatterns    = rulePatterns(func(rule routeRule) bool { return rule.ci })
)

// RoutePolicy enforces routeRules. Unregistered /v1 routes deny.
func RoutePolicy(next http.Handler) http.Handler {
	mux := http.NewServeMux()
	for _, rule := range routeRules {
		if rule.permission != "" {
			mux.HandleFunc(rule.pattern, Permit(rule.permission, next.ServeHTTP))
		}
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { apierror.Write(w, r, 403, permissionDeniedMessage) })
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if actor, ok := ActorFromContext(r.Context()); ok && actor.Role == CI {
			if ciPatterns[r.Method+" "+r.URL.Path] {
				next.ServeHTTP(w, r)
			} else {
				apierror.Write(w, r, http.StatusForbidden, permissionDeniedMessage)
			}
			return
		}
		if identityPaths[r.URL.Path] || !strings.HasPrefix(r.URL.Path, "/v1/") {
			next.ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
}
