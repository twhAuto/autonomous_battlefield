// Serves account-scoped background-generation availability, admission, and job status
// through the authenticated HTTP API.

package generation

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	"github.com/twh09/autonomous_battlefield/backend/internal/httpapi"
)

type Handler struct {
	Repository Repository
	Config     Config
	Profiles   *ai.ProfileRegistry
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/faction-background-generations/options", h.options)
	mux.HandleFunc("POST /v1/faction-background-generations", h.create)
	mux.HandleFunc("GET /v1/faction-background-generations/{id}", h.get)
}
func actor(w http.ResponseWriter, r *http.Request) (string, bool) {
	a, ok := authn.ActorFromContext(r.Context())
	if !ok {
		http.Error(w, "authentication required", 401)
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
		http.Error(w, "could not load generation allowance", 500)
		return
	}
	p, available := h.availability()
	available = available && ai.ProfileAllowed(r.Context(), p)
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
		http.Error(w, "invalid request body", 400)
		return
	}
	input, err := body.Input.Normalized()
	if err != nil {
		http.Error(w, "invalid name, doctrine, or background", 400)
		return
	}
	p, available := h.availability()
	available = available && ai.ProfileAllowed(r.Context(), p)
	if !available {
		http.Error(w, "background generation is unavailable", 503)
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
		_ = httpapi.WriteJSON(w, 429, o)
	case errors.Is(err, ErrConflict):
		http.Error(w, err.Error(), 409)
	case errors.Is(err, ErrInvalid):
		http.Error(w, err.Error(), 400)
	case err != nil:
		http.Error(w, "could not queue background generation", 500)
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
		http.Error(w, "generation not found", 404)
		return
	}
	if err != nil {
		http.Error(w, "could not load generation", 500)
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
