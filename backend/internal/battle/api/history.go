// Parses battle-history filters and pagination parameters for the service and returns
// summary pages over HTTP.

package api

import (
	"errors"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
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
	if authn.IsPlayer(r.Context()) {
		for i := range page.Battles {
			for j := range page.Battles[i].Participants {
				p := &page.Battles[i].Participants[j]
				p.Controller = game.ControllerConfig{Type: p.Controller.Type}
			}
		}
	}
	_ = httpapi.WriteJSON(w, http.StatusOK, page)
}
