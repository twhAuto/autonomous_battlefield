package authn

import "net/http"

// RolePreview applies real read permissions using the administrator's own identity.
// Preview never permits mutations and never elevates a non-administrator.
func RolePreview(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role := r.Header.Get("X-Battlefield-Preview-Role")
		if role == "" {
			next.ServeHTTP(w, r)
			return
		}
		actor, ok := ActorFromContext(r.Context())
		if !ok || actor.Role != Admin {
			http.Error(w, "administrator preview required", 403)
			return
		}
		if role != "player" && role != "developer" {
			http.Error(w, "invalid preview role", 400)
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			http.Error(w, "role preview is read-only", 403)
			return
		}
		actor.Role = Role(role)
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r.WithContext(WithActor(r.Context(), actor)))
	})
}
