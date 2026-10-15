// Serves account-scoped background-generation availability, admission, and job status
// through the authenticated HTTP API.

package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/profile"
	"github.com/twh09/autonomous_battlefield/backend/internal/apierror"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	"github.com/twh09/autonomous_battlefield/backend/internal/generation"
	"github.com/twh09/autonomous_battlefield/backend/internal/httpapi"
)

type Handler struct {
	repository generation.Repository
	config     generation.Config
	profiles   *profile.Registry
}

// NewHandler serves generation routes; a nil profile registry leaves generation unavailable.
func NewHandler(repository generation.Repository, config generation.Config, profiles *profile.Registry) *Handler {
	return &Handler{repository: repository, config: config, profiles: profiles}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/faction-background-generations/options", h.options)
	mux.HandleFunc("POST /v1/faction-background-generations", h.create)
	mux.HandleFunc("GET /v1/faction-background-generations/{id}", h.get)
}
func actor(w http.ResponseWriter, r *http.Request) (string, bool) {
	a, ok := authn.ActorFromContext(r.Context())
	if !ok {
		apierror.Write(w, r, http.StatusUnauthorized, "authentication required")
		return "", false
	}
	return a.ID, true
}
func (h *Handler) availability() (ai.ModelProfile, bool) {
	if !h.config.Enabled || h.profiles == nil {
		return ai.ModelProfile{}, false
	}
	p, err := h.profiles.Require(h.config.ProfileID)
	return p, err == nil && (p.Runtime == "api" || p.Runtime == "cli")
}
func (h *Handler) options(w http.ResponseWriter, r *http.Request) {
	id, ok := actor(w, r)
	if !ok {
		return
	}
	o, err := h.repository.Options(r.Context(), id, h.config)
	if err != nil {
		logFailure(r, "generation_options_failed", err)
		apierror.Write(w, r, http.StatusInternalServerError, "could not load generation allowance")
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
	_ = httpapi.WriteJSON(w, http.StatusOK, o)
}
func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	id, ok := actor(w, r)
	if !ok {
		return
	}
	var body struct {
		generation.Input
		IdempotencyKey string `json:"idempotencyKey"`
	}
	if httpapi.ReadJSON(w, r, &body) != nil {
		apierror.Write(w, r, http.StatusBadRequest, "invalid request body")
		return
	}
	input, err := body.Input.Normalized()
	if err != nil {
		apierror.Write(w, r, http.StatusBadRequest, "invalid name, doctrine, or background")
		return
	}
	p, available := h.availability()
	available = available && profile.Allowed(r.Context(), p)
	if !available {
		apierror.Write(w, r, http.StatusServiceUnavailable, "background generation is unavailable")
		return
	}
	model := generation.Admission{ProfileID: h.config.ProfileID, ModelLabel: p.ModelLabel, Runtime: p.Runtime}
	j, o, err := h.repository.Admit(r.Context(), id, body.IdempotencyKey, input, h.config, model)
	switch {
	case errors.Is(err, generation.ErrLimited):
		o.Available = true
		o.ModelLabel = p.ModelLabel
		w.Header().Set("Retry-After", strconv.Itoa(max(1, int(time.Until(o.RetryAt).Seconds())+1)))
		// Clients read the cooldown fields from this body, so the envelope is added
		// alongside them rather than replacing them.
		_ = httpapi.WriteJSON(w, http.StatusTooManyRequests, struct {
			generation.Options
			Error apierror.ErrorBody `json:"error"`
		}{o, apierror.Body(r, http.StatusTooManyRequests, generation.ErrLimited.Error())})
	case errors.Is(err, generation.ErrConflict):
		// Echo the sentinel, not err: a wrapped error would carry internal context.
		apierror.Write(w, r, http.StatusConflict, generation.ErrConflict.Error())
	case errors.Is(err, generation.ErrInvalid):
		apierror.Write(w, r, http.StatusBadRequest, generation.ErrInvalid.Error())
	case err != nil:
		logFailure(r, "generation_admit_failed", err)
		apierror.Write(w, r, http.StatusInternalServerError, "could not queue background generation")
	default:
		slog.InfoContext(r.Context(), "generation_queued", "request_id", httpapi.RequestID(r.Context()), "generation_id", j.ID)
		_ = httpapi.WriteJSON(w, http.StatusAccepted, responseFor(r, j))
	}
}
func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := actor(w, r)
	if !ok {
		return
	}
	j, err := h.repository.Get(r.Context(), id, r.PathValue("id"))
	if errors.Is(err, generation.ErrNotFound) {
		apierror.Write(w, r, http.StatusNotFound, "generation not found")
		return
	}
	if err != nil {
		logFailure(r, "generation_read_failed", err)
		apierror.Write(w, r, http.StatusInternalServerError, "could not load generation")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	_ = httpapi.WriteJSON(w, http.StatusOK, responseFor(r, j))
}

func logFailure(r *http.Request, event string, err error) {
	slog.ErrorContext(r.Context(), event, "request_id", httpapi.RequestID(r.Context()), "error", err)
}

func responseFor(r *http.Request, j generation.Job) any {
	if !authn.IsPlayer(r.Context()) {
		return j
	}
	return map[string]any{"id": j.ID, "status": j.Status, "background": j.Background, "modelLabel": j.ModelLabel, "createdAt": j.CreatedAt, "expiresAt": j.ExpiresAt}
}
