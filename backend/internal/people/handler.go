package people

import (
	"context"
	"errors"
	"net/http"

	"github.com/twh09/autonomous_battlefield/backend/internal/apierror"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	"github.com/twh09/autonomous_battlefield/backend/internal/httpapi"
)

// Names saves the caller's display name.
type Names interface {
	SetName(ctx context.Context, actor authn.Actor, name string) (string, error)
}

// Handler serves PUT /v1/profile, where a Player, Tester or Admin sets their own
// display name.
type Handler struct {
	names Names
}

func NewHandler(names Names) *Handler {
	return &Handler{names: names}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("PUT /v1/profile", authn.Permit(authn.PermissionPlay, h.setName))
}

func (h *Handler) setName(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DisplayName string `json:"displayName"`
	}
	if httpapi.ReadJSON(w, r, &body) != nil {
		apierror.Write(w, r, http.StatusBadRequest, "invalid request body")
		return
	}
	actor, _ := authn.ActorFromContext(r.Context())
	name, err := h.names.SetName(r.Context(), actor, body.DisplayName)
	switch {
	case errors.Is(err, ErrInvalidName):
		apierror.Write(w, r, http.StatusBadRequest, ErrInvalidName.Error())
	case errors.Is(err, ErrNameTaken):
		apierror.Write(w, r, http.StatusConflict, ErrNameTaken.Error())
	case errors.Is(err, authn.ErrForbidden):
		apierror.Write(w, r, http.StatusForbidden, "permission denied")
	case err != nil:
		logFailure(r.Context(), "display_name_save_failed", err)
		apierror.Write(w, r, http.StatusServiceUnavailable, "Your display name could not be saved. Try again.")
	default:
		_ = httpapi.WriteJSON(w, http.StatusOK, map[string]string{"displayName": name})
	}
}
