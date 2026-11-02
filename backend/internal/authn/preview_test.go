package authn

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRolePreviewBoundaries(t *testing.T) {
	for _, tt := range []struct {
		name                  string
		actor                 Role
		preview, method, path string
		status                int
		effective             Role
	}{
		{"normal admin", Admin, "", "GET", "/v1/model-profiles", 200, Admin},
		{"player preview", Admin, "player", "GET", "/v1/model-profiles", 200, Player},
		{"tester preview", Admin, "tester", "GET", "/v1/battles/x/decisions/r/context", 200, Tester},
		{"player decision history", Admin, "player", "GET", "/v1/battles/x/decisions", 200, Player},
		{"player inspection denied", Admin, "player", "GET", "/v1/battles/x/decisions/r/context", 403, ""},
		{"preview admin denied", Admin, "player", "GET", "/v1/admin/model-access", 403, ""},
		{"preview mutation denied", Admin, "tester", "PUT", "/v1/admin/model-access", 403, ""},
		{"player cannot preview", Player, "tester", "GET", "/v1/model-profiles", 403, ""},
		{"tester cannot preview", Tester, "player", "GET", "/v1/model-profiles", 403, ""},
		{"anonymous cannot preview", "", "player", "GET", "/v1/model-profiles", 403, ""},
		{"invalid preview", Admin, "admin", "GET", "/v1/model-profiles", 400, ""},
		{"player admin settings denied", Player, "", "GET", "/v1/admin/model-access", 403, ""},
		{"admin settings allowed", Admin, "", "PUT", "/v1/admin/model-access", 200, Admin},
	} {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			handler := RolePreview(RoutePolicy(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				actor, _ := ActorFromContext(r.Context())
				if actor.Role != tt.effective || actor.ID != "self" {
					t.Fatalf("actor=%+v", actor)
				}
				w.WriteHeader(200)
			})))
			r := httptest.NewRequest(tt.method, tt.path, nil)
			if tt.actor != "" {
				r = r.WithContext(WithActor(r.Context(), Actor{ID: "self", Role: tt.actor}))
			}
			r.Header.Set("X-Battlefield-Preview-Role", tt.preview)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tt.status || called != (tt.status == 200) {
				t.Fatalf("status=%d called=%v", w.Code, called)
			}
		})
	}
}
