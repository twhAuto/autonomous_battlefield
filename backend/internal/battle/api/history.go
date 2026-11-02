// Parses battle-history filters and pagination parameters for the service and returns
// summary pages over HTTP.

package api

import (
	"net/http"
	"strconv"

	"github.com/twh09/autonomous_battlefield/backend/internal/apierror"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"

	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/httpapi"
)

func (h *Handler) ListBattles(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit := 25
	if query.Has("limit") {
		parsed, err := strconv.Atoi(query.Get("limit"))
		if err != nil {
			apierror.Write(w, r, http.StatusBadRequest, "invalid battle limit")
			return
		}
		limit = parsed
	}
	page, err := h.battles.ListBattles(r.Context(), battleservice.BattleHistoryInput{Status: query.Get("status"), Cursor: query.Get("cursor"), Limit: limit})
	if writeError(w, r, err, []errorCase{invalidError("invalid battle history query")},
		failure{event: "battle_history_read_failed", message: "could not list battles"}) {
		return
	}
	if authn.GameplayOnly(r.Context()) {
		for i := range page.Battles {
			for j := range page.Battles[i].Participants {
				p := &page.Battles[i].Participants[j]
				p.Controller = game.ControllerConfig{Type: p.Controller.Type}
			}
		}
	}
	_ = httpapi.WriteJSON(w, http.StatusOK, page)
}
