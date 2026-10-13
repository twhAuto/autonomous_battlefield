// Serves account-scoped background-generation availability, admission, and job status
// through the authenticated HTTP API.

package generation

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/profile"
	"github.com/twh09/autonomous_battlefield/backend/internal/apierror"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	"github.com/twh09/autonomous_battlefield/backend/internal/httpapi"
)

type Handler struct {
	Repository Repository
	Config     Config
	Profiles   *profile.Registry
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/faction-background-generations/options", h.options)
	mux.HandleFunc("POST /v1/faction-background-generations", h.create)
	mux.HandleFunc("GET /v1/faction-background-generations/{id}", h.get)
}
func actor(w http.ResponseWriter, r *http.Request) (string, bool) {
	a, ok := authn.ActorFromContext(r.Context())
	if !ok {
		apierror.Write(w, r, 401, "authentication required")
		return "", false
	}
	return a.ID, true
}
func (h *Handler) availability() (ai.ModelProfile, bool) {
	if !h.Config.Enabled || h.Profiles == nil {
		return ai.ModelProfile{}, false
	}
	p, err := h.Profiles.Require(h.Config.ProfileID)
	return p, err == nil && (p.Runtime == "api" || p.Runtime == "cli")
}
func (h *Handler) options(w http.ResponseWriter, r *http.Request) {
	id, ok := actor(w, r)
	if !ok {
		return
	}
	o, err := h.Repository.Options(r.Context(), id, h.Config)
	if err != nil {
		apierror.Write(w, r, 500, "could not load generation allowance")
		return
	}
	p, available := h.availability()
	available = available && profile.Allowed(r.Context(), p)
	o.Available = available
	o.ModelLabel = p.ModelLabel
	if o.ModelLabel == "" {
		o.ModelLabel = p.DisplayName
	}
	if !available {
		o.Reason = "Background generation is unavailable. You can still write a background."
	}
	w.Header().Set("Cache-Control", "no-store")
	_ = httpapi.WriteJSON(w, 200, o)
}
func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	id, ok := actor(w, r)
	if !ok {
		return
	}
	var body struct {
		Input
		IdempotencyKey string `json:"idempotencyKey"`
	}
	if httpapi.ReadJSON(w, r, &body) != nil {
		apierror.Write(w, r, 400, "invalid request body")
		return
	}
	input, err := body.Input.Normalized()
	if err != nil {
		apierror.Write(w, r, 400, "invalid name, doctrine, or background")
		return
	}
	p, available := h.availability()
	available = available && profile.Allowed(r.Context(), p)
	if !available {
		apierror.Write(w, r, 503, "background generation is unavailable")
		return
	}
	admission := h.Config
	admission.Runtime = p.Runtime
	j, o, err := h.Repository.Admit(r.Context(), id, body.IdempotencyKey, input, admission, p.ModelLabel)
	switch {
	case errors.Is(err, ErrLimited):
		o.Available = true
		o.ModelLabel = p.ModelLabel
		w.Header().Set("Retry-After", strconv.Itoa(max(1, int(time.Until(o.RetryAt).Seconds())+1)))
		// Clients read the cooldown fields from this body, so the envelope is added
		// alongside them rather than replacing them.
		_ = httpapi.WriteJSON(w, 429, struct {
			Options
			Error apierror.ErrorBody `json:"error"`
		}{o, apierror.Body(r, 429, ErrLimited.Error())})
	case errors.Is(err, ErrConflict):
		// Echo the sentinel, not err: a wrapped error would carry internal context.
		apierror.Write(w, r, 409, ErrConflict.Error())
	case errors.Is(err, ErrInvalid):
		apierror.Write(w, r, 400, ErrInvalid.Error())
	case err != nil:
		apierror.Write(w, r, 500, "could not queue background generation")
	default:
		_ = httpapi.WriteJSON(w, 202, responseFor(r, j))
	}
}
func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := actor(w, r)
	if !ok {
		return
	}
	j, err := h.Repository.Get(r.Context(), id, r.PathValue("id"))
	if errors.Is(err, ErrNotFound) {
		apierror.Write(w, r, 404, "generation not found")
		return
	}
	if err != nil {
		apierror.Write(w, r, 500, "could not load generation")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	_ = httpapi.WriteJSON(w, 200, responseFor(r, j))
}

func responseFor(r *http.Request, j Job) any {
	if !authn.IsPlayer(r.Context()) {
		return j
	}
	return map[string]any{"id": j.ID, "status": j.Status, "background": j.Background, "modelLabel": j.ModelLabel, "createdAt": j.CreatedAt, "expiresAt": j.ExpiresAt}
}
