package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/twh09/autonomous_battlefield/backend/internal/service"
)

type FactionHandler struct {
	factionService service.FactionService
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

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"name": name}); err != nil {
		return
	}
}

func NewFactionHandler(factionService service.FactionService) *FactionHandler {
	return &FactionHandler{
		factionService: factionService,
	}
}
