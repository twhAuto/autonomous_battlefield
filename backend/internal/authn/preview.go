package authn

import (
	"net/http"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/apierror"
)

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
			apierror.Write(w, r, 403, "administrator preview required")
			return
		}
		if role != string(Player) && role != string(Tester) && role != string(Guest) {
			apierror.Write(w, r, 400, "invalid preview role")
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			apierror.Write(w, r, 403, "role preview is read-only")
			return
		}
		actor.Role = Role(role)
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r.WithContext(WithActor(r.Context(), actor)))
	})
}
