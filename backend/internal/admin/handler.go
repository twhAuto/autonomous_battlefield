// Exposes administrator model-access settings through HTTP.
package admin

import (
	"errors"
	"net/http"

	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	"github.com/twh09/autonomous_battlefield/backend/internal/httpapi"
)

type Handler struct{ Service *Service }

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/admin/model-access", authn.Permit("admin", h.get))
	mux.HandleFunc("PUT /v1/admin/model-access", authn.Permit("admin", h.save))
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	settings, err := h.Service.Get(r.Context())
	if err != nil {
		http.Error(w, "could not load model access settings", 503)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	_ = httpapi.WriteJSON(w, 200, settings)
}

func (h *Handler) save(w http.ResponseWriter, r *http.Request) {
	var settings Settings
	if httpapi.ReadJSON(w, r, &settings) != nil {
		http.Error(w, "invalid model access settings", 400)
		return
	}
	actor, _ := authn.ActorFromContext(r.Context())
	saved, err := h.Service.Save(r.Context(), settings, actor.ID)
	switch {
	case errors.Is(err, ErrInvalid):
		http.Error(w, "invalid model access settings", 400)
	case errors.Is(err, ErrConflict):
		http.Error(w, "settings changed; refresh before saving again", 409)
	case err != nil:
		http.Error(w, "could not save model access settings", 503)
	default:
		w.Header().Set("Cache-Control", "no-store")
		_ = httpapi.WriteJSON(w, 200, saved)
	}
}
