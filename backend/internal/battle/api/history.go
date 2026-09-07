package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/httpapi"
)

func (h *Handler) ListBattles(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit := 25
	if query.Has("limit") {
		parsed, err := strconv.Atoi(query.Get("limit"))
		if err != nil {
			http.Error(w, "invalid battle limit", http.StatusBadRequest)
			return
		}
		limit = parsed
	}
	page, err := h.battles.ListBattles(r.Context(), battleservice.BattleHistoryInput{Status: query.Get("status"), Cursor: query.Get("cursor"), Limit: limit})
	if errors.Is(err, battleservice.ErrInvalidBattle) {
		http.Error(w, "invalid battle history query", http.StatusBadRequest)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "battle_history_read_failed", "request_id", httpapi.RequestID(r.Context()), "error", err)
		http.Error(w, "could not list battles", http.StatusInternalServerError)
		return
	}
	_ = httpapi.WriteJSON(w, http.StatusOK, page)
}
