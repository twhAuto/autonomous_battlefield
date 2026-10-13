package authn

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPermissionMiddlewareRejectsUnknownAndAnonymous(t *testing.T) {
	for _, tt := range []struct {
		name       string
		permission Permission
		role       Role
		status     int
	}{
		{"anonymous", "play", "", 401}, {"unknown permission", "future", Admin, 403},
		{"player admin", "admin", Player, 403}, {"developer admin", "admin", Developer, 403},
		{"admin", "admin", Admin, 204}, {"unknown role", "play", "root", 403},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/v1/factions", nil)
			if tt.role != "" {
				r = r.WithContext(WithActor(r.Context(), Actor{ID: "friend", Role: tt.role}))
			}
			w := httptest.NewRecorder()
			Permit(tt.permission, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })(w, r)
			if w.Code != tt.status {
				t.Fatalf("status=%d want %d", w.Code, tt.status)
			}
		})
	}
}

func TestBrowserMutationBoundary(t *testing.T) {
	for _, tt := range []struct {
		name, origin, content string
		status                int
	}{
		{"missing origin", "", "application/json", 403},
		{"cross origin", "https://attacker.example", "application/json", 403},
		{"form submission", "https://play.example", "application/x-www-form-urlencoded", 415},
		{"JSON mutation", "https://play.example", "application/json; charset=utf-8", 204},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/v1/factions", strings.NewReader(`{}`))
			r.Header.Set("Origin", tt.origin)
			r.Header.Set("Content-Type", tt.content)
			w := httptest.NewRecorder()
			BrowserProtection("https://play.example", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })).ServeHTTP(w, r)
			if w.Code != tt.status {
				t.Fatalf("status=%d want %d", w.Code, tt.status)
			}
		})
	}
}

func TestRouteRegistryDeniesUnregisteredEndpoints(t *testing.T) {
	for _, tt := range []struct {
		path   string
		status int
	}{
		{"/v1/future-admin-endpoint", 403}, {"/v1/auth/session", 204}, {"/healthz", 204},
	} {
		t.Run(tt.path, func(t *testing.T) {
			r := httptest.NewRequest("GET", tt.path, nil)
			r = r.WithContext(WithActor(r.Context(), Actor{ID: "owner", Role: Admin}))
			w := httptest.NewRecorder()
			RoutePolicy(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })).ServeHTTP(w, r)
			if w.Code != tt.status {
				t.Fatalf("status=%d want %d", w.Code, tt.status)
			}
		})
	}
}

// Guards on the access policy table itself, so a careless edit cannot widen access.
func TestRouteRulesStayConsistent(t *testing.T) {
	seen := map[string]bool{}
	for _, rule := range routeRules {
		method, path, ok := strings.Cut(rule.pattern, " ")
		if !ok || method == "" || !strings.HasPrefix(path, "/v1/") {
			t.Errorf("%q: pattern must be \"METHOD /v1/...\"", rule.pattern)
		}
		if seen[rule.pattern] {
			t.Errorf("%q: listed twice", rule.pattern)
		}
		seen[rule.pattern] = true
		// Identity routes skip the permission check, so only authentication routes may.
		if rule.permission == "" && !strings.HasPrefix(path, "/v1/auth/") {
			t.Errorf("%q: only /v1/auth/ routes may omit a permission", rule.pattern)
		}
		if rule.public && rule.permission != "" {
			t.Errorf("%q: a public route cannot also require a permission", rule.pattern)
		}
		// CI matches the exact method and path, so a wildcard would never match.
		if rule.ci && strings.ContainsAny(path, "{}") {
			t.Errorf("%q: CI routes must be literal paths", rule.pattern)
		}
	}
}
