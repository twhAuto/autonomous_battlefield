package authn

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPermissionMiddlewareRejectsUnknownAndAnonymous(t *testing.T) {
	for _, tt := range []struct {
		name, permission string
		role             Role
		status           int
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
