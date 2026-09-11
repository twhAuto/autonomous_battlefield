// Provides administrator model access settings backed by a versioned PostgreSQL
// policy. Provider credentials and executable model definitions remain deployment-owned.
package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	"github.com/twh09/autonomous_battlefield/backend/internal/httpapi"
	"github.com/twh09/autonomous_battlefield/backend/internal/postgres"
)

type Handler struct {
	DB       postgres.DBTX
	Profiles *ai.ProfileRegistry
}
type Settings struct {
	Version int64           `json:"version"`
	Policy  ai.AccessPolicy `json:"policy"`
}

func (h *Handler) read(ctx context.Context) (Settings, error) {
	var s Settings
	var raw []byte
	err := h.DB.QueryRow(ctx, "SELECT version,policy FROM model_access_policy WHERE id").Scan(&s.Version, &raw)
	if err != nil {
		return s, err
	}
	err = json.Unmarshal(raw, &s.Policy)
	return s, err
}
func (h *Handler) ReadPolicy() (ai.AccessPolicy, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	s, err := h.read(ctx)
	return s.Policy, err
}
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/admin/model-access", authn.Permit("admin", h.get))
	mux.HandleFunc("PUT /v1/admin/model-access", authn.Permit("admin", h.save))
}
func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	s, err := h.read(r.Context())
	if err != nil {
		http.Error(w, "could not load model access settings", 503)
		return
	}
	models := h.Profiles.List()
	stored := s.Policy
	s.Policy = ai.AccessPolicy{Providers: map[string]bool{}, Profiles: map[string]ai.ProfileAccess{}}
	for _, p := range models {
		if value, ok := stored.Providers[p.Provider]; ok {
			s.Policy.Providers[p.Provider] = value
		}
		if value, ok := stored.Profiles[p.ID]; ok {
			s.Policy.Profiles[p.ID] = value
		}
		if _, ok := s.Policy.Providers[p.Provider]; !ok {
			s.Policy.Providers[p.Provider] = true
		}
		if _, ok := s.Policy.Profiles[p.ID]; !ok {
			s.Policy.Profiles[p.ID] = defaultProfileAccess(p)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	_ = httpapi.WriteJSON(w, 200, map[string]any{"version": s.Version, "policy": s.Policy, "models": models})
}
func defaultProfileAccess(p ai.ModelProfile) ai.ProfileAccess {
	roles := p.AllowedRoles
	if len(roles) == 0 {
		roles = []string{"admin", "developer"}
	}
	filtered := []string{}
	for _, role := range roles {
		if p.Runtime != "cli" || role != "player" {
			filtered = append(filtered, role)
		}
	}
	return ai.ProfileAccess{Enabled: true, AllowedRoles: filtered}
}
func validRoles(roles []string, runtime string) bool {
	if roles == nil {
		return false
	}
	seen := map[string]bool{}
	for _, role := range roles {
		if (role != "admin" && role != "developer" && role != "player") || (role == "player" && runtime == "cli") || seen[role] {
			return false
		}
		seen[role] = true
	}
	return true
}
func (h *Handler) valid(s Settings) bool {
	models := h.Profiles.List()
	providers := map[string]bool{}
	if s.Version < 0 || len(s.Policy.Profiles) != len(models) {
		return false
	}
	for _, p := range models {
		providers[p.Provider] = true
		rule, ok := s.Policy.Profiles[p.ID]
		if !ok || !validRoles(rule.AllowedRoles, p.Runtime) {
			return false
		}
		if _, ok := s.Policy.Providers[p.Provider]; !ok {
			return false
		}
	}
	return len(s.Policy.Providers) == len(providers)
}
func (h *Handler) save(w http.ResponseWriter, r *http.Request) {
	var s Settings
	if httpapi.ReadJSON(w, r, &s) != nil || !h.valid(s) {
		http.Error(w, "invalid model access settings", 400)
		return
	}
	raw, err := json.Marshal(s.Policy)
	if err != nil {
		http.Error(w, "invalid model access settings", 400)
		return
	}
	actor, _ := authn.ActorFromContext(r.Context())
	err = h.DB.QueryRow(r.Context(), "UPDATE model_access_policy SET policy=$1,version=version+1,updated_by=$2,updated_at=now() WHERE id AND version=$3 RETURNING version", raw, actor.ID, s.Version).Scan(&s.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "settings changed; refresh before saving again", 409)
		return
	}
	if err != nil {
		http.Error(w, "could not save model access settings", 503)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	_ = httpapi.WriteJSON(w, 200, s)
}
