package members

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/apierror"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/authn"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/httpapi"
)

type Repository interface {
	List(context.Context) ([]Member, string, error)
	Change(ctx context.Context, actor, email, id string, grant bool, role authn.Role) error
	Retry(context.Context, string) error
}
type Handler struct {
	Store  Repository
	Origin string
}

// NewHandler serves the Admin member routes. A nil store, when managed Players are
// disabled, answers every route with 503.
func NewHandler(store Repository, origin string) *Handler {
	return &Handler{Store: store, Origin: origin}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	for pattern, handler := range map[string]http.HandlerFunc{
		"GET /v1/admin/members": h.list, "POST /v1/admin/members": h.grant,
		"DELETE /v1/admin/members/{id}": h.remove, "POST /v1/admin/members/retry": h.retry,
	} {
		mux.HandleFunc(pattern, authn.Permit(authn.PermissionAdmin, h.bounded(handler)))
	}
}
func (h *Handler) bounded(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if h.Store == nil {
			apierror.Write(w, r, http.StatusServiceUnavailable, "Player management is not configured")
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
		memberError(w, r, err)
		return
	}
	_ = httpapi.WriteJSON(w, http.StatusOK, map[string]any{"members": members, "syncError": lastError, "gameUrl": h.Origin})
}
func (h *Handler) grant(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string     `json:"email"`
		Role  authn.Role `json:"role"`
	}
	if httpapi.ReadJSON(w, r, &body) != nil {
		memberError(w, r, ErrInvalid)
		return
	}
	if !assignable(body.Role) {
		memberError(w, r, ErrInvalidRole)
		return
	}
	actor, _ := authn.ActorFromContext(r.Context())
	if err := h.Store.Change(r.Context(), actor.ID, body.Email, "", true, body.Role); err != nil {
		memberError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
func (h *Handler) remove(w http.ResponseWriter, r *http.Request) {
	actor, _ := authn.ActorFromContext(r.Context())
	if err := h.Store.Change(r.Context(), actor.ID, "", r.PathValue("id"), false, ""); err != nil {
		memberError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
func (h *Handler) retry(w http.ResponseWriter, r *http.Request) {
	actor, _ := authn.ActorFromContext(r.Context())
	if err := h.Store.Retry(r.Context(), actor.ID); err != nil {
		memberError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
func memberError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrInvalid):
		apierror.Write(w, r, http.StatusBadRequest, ErrInvalid.Error())
	case errors.Is(err, ErrInvalidRole):
		apierror.Write(w, r, http.StatusBadRequest, ErrInvalidRole.Error())
	case errors.Is(err, ErrProtected):
		apierror.Write(w, r, http.StatusConflict, ErrProtected.Error())
	case errors.Is(err, ErrNotFound):
		apierror.Write(w, r, http.StatusNotFound, ErrNotFound.Error())
	default:
		logRequestFailure(r, err)
		apierror.Write(w, r, http.StatusServiceUnavailable, "Member access could not be updated or loaded. Try again.")
	}
}

// logRequestFailure records a fixed category and SQLSTATE, never the raw error: a
// pgx connection error can name the database user, host and database.
func logRequestFailure(r *http.Request, err error) {
	attrs := []any{"request_id", httpapi.RequestID(r.Context()), "reason", failureReason(err)}
	var database *pgconn.PgError
	if errors.As(err, &database) {
		attrs = append(attrs, "sqlstate", database.Code)
	}
	slog.ErrorContext(r.Context(), "player_members_request_failed", attrs...)
}

func failureReason(err error) string {
	var database *pgconn.PgError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.As(err, &database):
		return "database_error"
	default:
		return "database_unavailable"
	}
}
