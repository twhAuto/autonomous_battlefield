package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/httpapi"
)

func (h *Handler) GetDecisionContext(w http.ResponseWriter, r *http.Request) {
	attempt, err := strconv.Atoi(r.URL.Query().Get("attempt"))
	if err != nil || attempt < 1 {
		http.Error(w, "invalid battle attempt", http.StatusBadRequest)
		return
	}
	service, ok := h.battles.(battleservice.DecisionContextService)
	if !ok {
		http.Error(w, "decision context not found", http.StatusNotFound)
		return
	}
	result, err := service.GetDecisionContext(r.Context(), r.PathValue("id"), r.PathValue("requestId"), attempt)
	switch {
	case errors.Is(err, battleservice.ErrInvalidBattle):
		http.Error(w, "invalid decision query", http.StatusBadRequest)
	case errors.Is(err, ai.ErrDecisionNotFound):
		http.Error(w, "decision context not found", http.StatusNotFound)
	case err != nil:
		http.Error(w, "could not load decision context", http.StatusInternalServerError)
	default:
		_ = httpapi.WriteJSON(w, http.StatusOK, result)
	}
}
