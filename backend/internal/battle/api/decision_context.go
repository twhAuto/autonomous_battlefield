package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/apierror"
	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/httpapi"
)

func (h *Handler) GetDecisionContext(w http.ResponseWriter, r *http.Request) {
	attempt, err := strconv.Atoi(r.URL.Query().Get("attempt"))
	if err != nil || attempt < 1 {
		apierror.Write(w, r, http.StatusBadRequest, "invalid battle attempt")
		return
	}
	service, ok := h.battles.(battleservice.DecisionContextService)
	if !ok {
		apierror.Write(w, r, http.StatusNotFound, "decision context not found")
		return
	}
	result, err := service.GetDecisionContext(r.Context(), r.PathValue("id"), r.PathValue("requestId"), attempt)
	switch {
	case errors.Is(err, battleservice.ErrInvalidBattle):
		apierror.Write(w, r, http.StatusBadRequest, "invalid decision query")
	case errors.Is(err, ai.ErrDecisionNotFound):
		apierror.Write(w, r, http.StatusNotFound, "decision context not found")
	case err != nil:
		apierror.Write(w, r, http.StatusInternalServerError, "could not load decision context")
	default:
		_ = httpapi.WriteJSON(w, http.StatusOK, result)
	}
}
