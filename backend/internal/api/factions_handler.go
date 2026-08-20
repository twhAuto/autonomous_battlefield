package api

import (
	"errors"
	"net/http"

	"github.com/twh09/autonomous_battlefield/backend/internal/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/service/entity"
)

type FactionHandler struct {
	factionService service.FactionService
}

func (h *FactionHandler) ListFactions(w http.ResponseWriter, r *http.Request) {
	factions, err := h.factionService.ListFactions(r.Context())
	if err != nil {
		http.Error(w, "could not list factions", http.StatusInternalServerError)
		return
	}

	type factionResponse struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	response := struct {
		Factions []factionResponse `json:"factions"`
	}{Factions: make([]factionResponse, 0, len(factions))}
	for _, faction := range factions {
		response.Factions = append(response.Factions, factionResponse{
			ID:   faction.ID,
			Name: faction.Name,
		})
	}

	if err := writeJSON(w, http.StatusOK, response); err != nil {
		return
	}
}

func (h *FactionHandler) GetFactionName(w http.ResponseWriter, r *http.Request) {
	name, err := h.factionService.GetFactionName(r.Context(), r.PathValue("id"))
	if errors.Is(err, service.ErrFactionNotFound) {
		http.Error(w, "faction not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "could not get faction", http.StatusInternalServerError)
		return
	}

	if err := writeJSON(w, http.StatusOK, map[string]string{"name": name}); err != nil {
		return
	}
}

func (h *FactionHandler) CreateFaction(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := readJSON(w, r, &request); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	err := h.factionService.CreateFaction(r.Context(), &entity.FactionEntity{
		ID:   request.ID,
		Name: request.Name,
	})
	if errors.Is(err, service.ErrInvalidFaction) {
		http.Error(w, "invalid faction", http.StatusBadRequest)
		return
	}
	if errors.Is(err, service.ErrFactionAlreadyExists) {
		http.Error(w, "faction already exists", http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, "could not create faction", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
}

func NewFactionHandler(factionService service.FactionService) *FactionHandler {
	return &FactionHandler{
		factionService: factionService,
	}
}
