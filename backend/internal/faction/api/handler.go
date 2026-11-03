// Translates faction profile and ruleset HTTP requests into service calls, JSON responses,
// and transport errors.

package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/twh09/autonomous_battlefield/backend/internal/apierror"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	"github.com/twh09/autonomous_battlefield/backend/internal/faction"
	factionservice "github.com/twh09/autonomous_battlefield/backend/internal/faction/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/httpapi"
)

type Handler struct {
	factionService factionservice.Service
	owners         OwnerNames
}

// OwnerNames labels the people who own factions. Without one, responses omit ownerName.
type OwnerNames interface {
	Labels(ctx context.Context, ownerIDs []string) (map[string]string, error)
}

func NewHandler(factionService factionservice.Service) *Handler {
	return &Handler{factionService: factionService}
}

// WithOwnerNames labels each listed faction with its owner.
func (h *Handler) WithOwnerNames(owners OwnerNames) *Handler {
	h.owners = owners
	return h
}

// factionResponse is a listed faction labelled with its owner.
type factionResponse struct {
	faction.Faction
	OwnerName string `json:"ownerName,omitempty"`
}

// labelled names every faction's owner with one lookup. A failed lookup leaves the
// names out rather than failing the list.
func (h *Handler) labelled(r *http.Request, factions []faction.Faction) []factionResponse {
	var labels map[string]string
	if h.owners != nil && len(factions) > 0 {
		owners := make([]string, len(factions))
		for i, f := range factions {
			owners[i] = f.OwnerID
		}
		var err error
		if labels, err = h.owners.Labels(r.Context(), owners); err != nil {
			slog.WarnContext(r.Context(), "owner_names_failed", "request_id", httpapi.RequestID(r.Context()), "error", err)
		}
	}
	result := make([]factionResponse, len(factions))
	for i, f := range factions {
		result[i] = factionResponse{Faction: f, OwnerName: labels[f.OwnerID]}
	}
	return result
}

const (
	permissionDeniedMessage = "permission denied"
	factionNotFoundMessage  = "faction not found"
	invalidBodyMessage      = "invalid request body"
)

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/factions", h.ListFactions)
	mux.HandleFunc("GET /v1/faction-rulesets", h.ListRulesets)
	mux.HandleFunc("POST /v1/factions", h.CreateFaction)
	mux.HandleFunc("PUT /v1/factions/{id}", h.UpdateFaction)
	mux.HandleFunc("DELETE /v1/factions/{id}", h.DeleteFaction)
}

func (h *Handler) ListFactions(w http.ResponseWriter, r *http.Request) {
	factions, err := h.factionService.ListFactions(r.Context())
	if err != nil {
		slog.ErrorContext(r.Context(), "faction_list_failed", "request_id", httpapi.RequestID(r.Context()), "error", err)
		apierror.Write(w, r, http.StatusInternalServerError, "could not list factions")
		return
	}
	response := struct {
		Factions any `json:"factions"`
	}{Factions: h.labelled(r, factions)}

	if err := httpapi.WriteJSON(w, http.StatusOK, response); err != nil {
		return
	}
}

func (h *Handler) ListRulesets(w http.ResponseWriter, _ *http.Request) {
	_ = httpapi.WriteJSON(w, http.StatusOK, map[string]any{"rulesets": h.factionService.ListRulesets()})
}

func (h *Handler) CreateFaction(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Doctrine   string `json:"doctrine"`
		Background string `json:"background"`
		Emblem     string `json:"emblem"`
	}
	if err := httpapi.ReadJSON(w, r, &request); err != nil {
		apierror.Write(w, r, http.StatusBadRequest, invalidBodyMessage)
		return
	}

	err := h.factionService.CreateFaction(r.Context(), factionservice.ProfileInput{
		ID: request.ID, Name: request.Name, Doctrine: request.Doctrine, Background: request.Background, Emblem: request.Emblem,
	})
	if errors.Is(err, authn.ErrForbidden) {
		apierror.Write(w, r, http.StatusForbidden, permissionDeniedMessage)
		return
	}
	if errors.Is(err, factionservice.ErrInvalidFaction) {
		apierror.Write(w, r, http.StatusBadRequest, "invalid faction")
		return
	}
	if errors.Is(err, factionservice.ErrFactionAlreadyExists) {
		apierror.WriteCode(w, r, http.StatusConflict, apierror.CodeAlreadyExists, "faction already exists")
		return
	}
	if errors.Is(err, factionservice.ErrFactionLimitReached) {
		apierror.Write(w, r, http.StatusConflict, "players can own only one faction at a time")
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "faction_create_failed", "request_id", httpapi.RequestID(r.Context()), "faction_id", request.ID, "error", err)
		apierror.Write(w, r, http.StatusInternalServerError, "could not create faction")
		return
	}

	slog.InfoContext(r.Context(), "faction_created", "request_id", httpapi.RequestID(r.Context()), "faction_id", request.ID)
	w.WriteHeader(http.StatusCreated)
}

func (h *Handler) DeleteFaction(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := h.factionService.DeleteFaction(r.Context(), id)
	if errors.Is(err, authn.ErrForbidden) {
		apierror.Write(w, r, http.StatusForbidden, permissionDeniedMessage)
		return
	}
	if errors.Is(err, factionservice.ErrInvalidFaction) {
		apierror.Write(w, r, http.StatusBadRequest, "invalid faction ID")
		return
	}
	if errors.Is(err, factionservice.ErrFactionNotFound) {
		apierror.Write(w, r, http.StatusNotFound, factionNotFoundMessage)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "faction_delete_failed", "request_id", httpapi.RequestID(r.Context()), "faction_id", id, "error", err)
		apierror.Write(w, r, http.StatusInternalServerError, "could not delete faction")
		return
	}
	slog.InfoContext(r.Context(), "faction_deleted", "request_id", httpapi.RequestID(r.Context()), "faction_id", id)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) UpdateFaction(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name       string `json:"name"`
		Doctrine   string `json:"doctrine"`
		Background string `json:"background"`
		Emblem     string `json:"emblem"`
	}
	if err := httpapi.ReadJSON(w, r, &request); err != nil {
		apierror.Write(w, r, http.StatusBadRequest, invalidBodyMessage)
		return
	}
	id := r.PathValue("id")
	err := h.factionService.UpdateFaction(r.Context(), factionservice.ProfileInput{ID: id, Name: request.Name, Doctrine: request.Doctrine, Background: request.Background, Emblem: request.Emblem})
	switch {
	case errors.Is(err, authn.ErrForbidden):
		apierror.Write(w, r, http.StatusForbidden, permissionDeniedMessage)
	case errors.Is(err, factionservice.ErrInvalidFaction):
		apierror.Write(w, r, http.StatusBadRequest, "invalid faction")
	case errors.Is(err, factionservice.ErrFactionNotFound):
		apierror.Write(w, r, http.StatusNotFound, factionNotFoundMessage)
	case err != nil:
		slog.ErrorContext(r.Context(), "faction_update_failed", "request_id", httpapi.RequestID(r.Context()), "faction_id", id, "error", err)
		apierror.Write(w, r, http.StatusInternalServerError, "could not update faction")
	default:
		slog.InfoContext(r.Context(), "faction_updated", "request_id", httpapi.RequestID(r.Context()), "faction_id", id)
		w.WriteHeader(http.StatusNoContent)
	}
}
