package api

import (
	"net/http"
	"strconv"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/apierror"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/httpapi"
)

func (h *Handler) GetDecisionContext(w http.ResponseWriter, r *http.Request) {
	attempt, err := strconv.Atoi(r.URL.Query().Get("attempt"))
	if err != nil || attempt < 1 {
		apierror.Write(w, r, http.StatusBadRequest, "invalid battle attempt")
		return
	}
	result, err := h.battles.GetDecisionContext(r.Context(), r.PathValue("id"), r.PathValue("requestId"), attempt)
	if writeError(w, r, err, []errorCase{invalidError("invalid decision query"), errorFor(http.StatusNotFound, "decision context not found", ai.ErrDecisionNotFound)},
		failure{message: "could not load decision context"}) {
		return
	}
	_ = httpapi.WriteJSON(w, http.StatusOK, result)
}
