// Parses battle-history filters and pagination parameters for the service and returns
// summary pages, and the rules versions those pages can be filtered by, over HTTP.

package api

import (
	"net/http"
	"strconv"

	"github.com/twh09/autonomous_battlefield/backend/internal/apierror"

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
	page, err := h.battles.ListBattles(r.Context(), battleservice.BattleHistoryInput{Status: query.Get("status"), RulesVersion: query.Get("rulesVersion"), Cursor: query.Get("cursor"), Limit: limit})
	if writeError(w, r, err, []errorCase{invalidError("invalid battle history query")},
		failure{event: "battle_history_read_failed", message: "could not list battles"}) {
		return
	}
	h.labelOwners(r, page.Battles)
	_ = httpapi.WriteJSON(w, http.StatusOK, page)
}

func (h *Handler) ListRulesVersions(w http.ResponseWriter, r *http.Request) {
	versions, err := h.battles.ListRulesVersions(r.Context())
	if writeError(w, r, err, nil, failure{event: "rules_versions_read_failed", message: "could not list rules versions"}) {
		return
	}
	_ = httpapi.WriteJSON(w, http.StatusOK, versions)
}

// labelOwners names every row's owner with one lookup for the page.
func (h *Handler) labelOwners(r *http.Request, battles []battleservice.BattleSummary) {
	owners := make([]string, len(battles))
	for i, battle := range battles {
		owners[i] = battle.OwnerID
	}
	labels := h.ownerNames(r, owners)
	for i := range battles {
		battles[i].OwnerName = labels[battles[i].OwnerID]
	}
}
