// Exposes administrator model-access settings through HTTP.
package admin

import (
	"errors"
	"net/http"

	"github.com/twh09/autonomous_battlefield/backend/internal/apierror"
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
		apierror.Write(w, r, 503, "could not load model access settings")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	_ = httpapi.WriteJSON(w, 200, settings)
}

func (h *Handler) save(w http.ResponseWriter, r *http.Request) {
	var settings Settings
	if httpapi.ReadJSON(w, r, &settings) != nil {
		apierror.Write(w, r, 400, "invalid model access settings")
		return
	}
	actor, _ := authn.ActorFromContext(r.Context())
	saved, err := h.Service.Save(r.Context(), settings, actor.ID)
	switch {
	case errors.Is(err, ErrInvalid):
		apierror.Write(w, r, 400, "invalid model access settings")
	case errors.Is(err, ErrConflict):
		apierror.Write(w, r, 409, "settings changed; refresh before saving again")
	case err != nil:
		apierror.Write(w, r, 503, "could not save model access settings")
	default:
		w.Header().Set("Cache-Control", "no-store")
		_ = httpapi.WriteJSON(w, 200, saved)
	}
}
