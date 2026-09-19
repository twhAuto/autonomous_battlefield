package members

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	"github.com/twh09/autonomous_battlefield/backend/internal/httpapi"
)

type Repository interface {
	List(context.Context) ([]Member, string, error)
	Change(context.Context, string, string, string, bool) error
	Retry(context.Context, string) error
}
type Handler struct {
	Store  Repository
	Origin string
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	for pattern, handler := range map[string]http.HandlerFunc{
		"GET /v1/admin/members": h.list, "POST /v1/admin/members": h.grant,
		"DELETE /v1/admin/members/{id}": h.remove, "POST /v1/admin/members/retry": h.retry,
	} {
		mux.HandleFunc(pattern, authn.Permit("admin", h.bounded(handler)))
	}
}
func (h *Handler) bounded(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if h.Store == nil {
			http.Error(w, "Player management is not configured", http.StatusServiceUnavailable)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		next(w, r.WithContext(ctx))
	}
}
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	members, lastError, err := h.Store.List(r.Context())
	if err != nil {
		memberError(w, err)
		return
	}
	_ = httpapi.WriteJSON(w, 200, map[string]any{"members": members, "syncError": lastError, "gameUrl": h.Origin})
}
func (h *Handler) grant(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		memberError(w, ErrInvalid)
		return
	}
	actor, _ := authn.ActorFromContext(r.Context())
	if err := h.Store.Change(r.Context(), actor.ID, body.Email, "", true); err != nil {
		memberError(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
func (h *Handler) remove(w http.ResponseWriter, r *http.Request) {
	actor, _ := authn.ActorFromContext(r.Context())
	if err := h.Store.Change(r.Context(), actor.ID, "", r.PathValue("id"), false); err != nil {
		memberError(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
func (h *Handler) retry(w http.ResponseWriter, r *http.Request) {
	actor, _ := authn.ActorFromContext(r.Context())
	if err := h.Store.Retry(r.Context(), actor.ID); err != nil {
		memberError(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
func memberError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalid):
		http.Error(w, ErrInvalid.Error(), 400)
	case errors.Is(err, ErrProtected):
		http.Error(w, ErrProtected.Error(), 409)
	case errors.Is(err, ErrNotFound):
		http.Error(w, ErrNotFound.Error(), 404)
	default:
		http.Error(w, "Member access could not be updated or loaded. Try again.", 503)
	}
}
