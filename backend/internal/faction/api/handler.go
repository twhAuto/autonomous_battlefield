package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/twh09/autonomous_battlefield/backend/internal/faction"
	factionservice "github.com/twh09/autonomous_battlefield/backend/internal/faction/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/httpapi"
)

type Handler struct {
	factionService factionservice.Service
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/factions", h.ListFactions)
	mux.HandleFunc("GET /v1/faction-rulesets", h.ListRulesets)
	mux.HandleFunc("GET /v1/factions/{id}/name", h.GetFactionName)
	mux.HandleFunc("POST /v1/factions", h.CreateFaction)
	mux.HandleFunc("DELETE /v1/factions/{id}", h.DeleteFaction)
}

func (h *Handler) ListFactions(w http.ResponseWriter, r *http.Request) {
	factions, err := h.factionService.ListFactions(r.Context())
	if err != nil {
		slog.ErrorContext(r.Context(), "faction_list_failed", "request_id", httpapi.RequestID(r.Context()), "error", err)
		http.Error(w, "could not list factions", http.StatusInternalServerError)
		return
	}
	if factions == nil {
		factions = []faction.Faction{}
	}

	response := struct {
		Factions any `json:"factions"`
	}{Factions: factions}

	if err := httpapi.WriteJSON(w, http.StatusOK, response); err != nil {
		return
	}
}

func (h *Handler) ListRulesets(w http.ResponseWriter, _ *http.Request) {
	lister, ok := h.factionService.(factionservice.RulesetLister)
	if !ok {
		_ = httpapi.WriteJSON(w, http.StatusOK, map[string]any{"rulesets": []game.Ruleset{}})
		return
	}
	_ = httpapi.WriteJSON(w, http.StatusOK, map[string]any{"rulesets": lister.ListRulesets()})
}

func (h *Handler) GetFactionName(w http.ResponseWriter, r *http.Request) {
	name, err := h.factionService.GetFactionName(r.Context(), r.PathValue("id"))
	if errors.Is(err, factionservice.ErrFactionNotFound) {
		http.Error(w, "faction not found", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "faction_read_failed", "request_id", httpapi.RequestID(r.Context()), "faction_id", r.PathValue("id"), "error", err)
		http.Error(w, "could not get faction", http.StatusInternalServerError)
		return
	}

	if err := httpapi.WriteJSON(w, http.StatusOK, map[string]string{"name": name}); err != nil {
		return
	}
}

func (h *Handler) CreateFaction(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ID        string         `json:"id"`
		Name      string         `json:"name"`
		RulesetID string         `json:"rulesetId"`
		Blueprint game.Blueprint `json:"blueprint"`
	}
	if err := httpapi.ReadJSON(w, r, &request); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	err := h.factionService.CreateFaction(r.Context(), factionservice.CreateInput{
		ID: request.ID, Name: request.Name, RulesetID: request.RulesetID, Blueprint: request.Blueprint,
	})
	if errors.Is(err, factionservice.ErrInvalidFaction) {
		http.Error(w, "invalid faction", http.StatusBadRequest)
		return
	}
	if errors.Is(err, factionservice.ErrInvalidBlueprint) {
		slog.WarnContext(r.Context(), "faction_blueprint_rejected",
			"request_id", httpapi.RequestID(r.Context()),
			"faction_id", request.ID,
			"ruleset_id", request.RulesetID,
			"error", err,
		)
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	if errors.Is(err, factionservice.ErrFactionAlreadyExists) {
		http.Error(w, "faction already exists", http.StatusConflict)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "faction_create_failed", "request_id", httpapi.RequestID(r.Context()), "faction_id", request.ID, "error", err)
		http.Error(w, "could not create faction", http.StatusInternalServerError)
		return
	}

	slog.InfoContext(r.Context(), "faction_created", "request_id", httpapi.RequestID(r.Context()), "faction_id", request.ID)
	w.WriteHeader(http.StatusCreated)
}

func (h *Handler) DeleteFaction(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := h.factionService.DeleteFaction(r.Context(), id)
	if errors.Is(err, factionservice.ErrInvalidFaction) {
		http.Error(w, "invalid faction ID", http.StatusBadRequest)
		return
	}
	if errors.Is(err, factionservice.ErrFactionNotFound) {
		http.Error(w, "faction not found", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "faction_delete_failed", "request_id", httpapi.RequestID(r.Context()), "faction_id", id, "error", err)
		http.Error(w, "could not delete faction", http.StatusInternalServerError)
		return
	}
	slog.InfoContext(r.Context(), "faction_deleted", "request_id", httpapi.RequestID(r.Context()), "faction_id", id)
	w.WriteHeader(http.StatusNoContent)
}

func NewHandler(factionService factionservice.Service) *Handler {
	return &Handler{
		factionService: factionService,
	}
}
