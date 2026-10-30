// Translates league HTTP requests into service calls and presents leagues with their
// group tables, knockout ties and fixtures.

package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/apierror"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/httpapi"
	"github.com/twh09/autonomous_battlefield/backend/internal/league"
	leagueservice "github.com/twh09/autonomous_battlefield/backend/internal/league/service"
)

type Handler struct {
	leagues *leagueservice.Service
}

func NewHandler(leagues *leagueservice.Service) *Handler {
	return &Handler{leagues: leagues}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/leagues", h.CreateLeague)
	mux.HandleFunc("GET /v1/leagues", h.ListLeagues)
	mux.HandleFunc("GET /v1/leagues/{id}", h.GetLeague)
	mux.HandleFunc("POST /v1/leagues/{id}/cancel", h.CancelLeague)
	mux.HandleFunc("POST /v1/leagues/{id}/entrants/{entrant}/disqualify", h.DisqualifyEntrant)
}

type controllerRequest struct {
	Type            string `json:"type"`
	ProfileID       string `json:"profileId,omitempty"`
	ModelIdentifier string `json:"modelIdentifier,omitempty"`
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
}

func (h *Handler) CreateLeague(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ID                    string   `json:"id"`
		Name                  string   `json:"name"`
		ScenarioIDs           []string `json:"scenarioIds"`
		TickCount             uint64   `json:"tickCount"`
		TickDuration          *string  `json:"tickDuration,omitempty"`
		DecisionIntervalTicks *uint64  `json:"decisionIntervalTicks,omitempty"`
		Entrants              []struct {
			FactionID  string            `json:"factionId"`
			Group      string            `json:"group"`
			Controller controllerRequest `json:"controller"`
		} `json:"entrants"`
	}
	if err := httpapi.ReadJSON(w, r, &request); err != nil {
		apierror.Write(w, r, http.StatusBadRequest, "invalid request body")
		return
	}
	input := leagueservice.CreateInput{ID: request.ID, Name: request.Name, ScenarioIDs: request.ScenarioIDs, TickCount: request.TickCount, TickDuration: request.TickDuration, DecisionIntervalTicks: request.DecisionIntervalTicks}
	for _, entrant := range request.Entrants {
		group, ok := groupIndex(entrant.Group)
		if !ok {
			apierror.Write(w, r, http.StatusBadRequest, invalidLeagueMessage)
			return
		}
		c := entrant.Controller
		input.Entrants = append(input.Entrants, leagueservice.EntrantInput{FactionID: entrant.FactionID, Group: group, Controller: game.ControllerConfig{Type: c.Type, ProfileID: c.ProfileID, ModelIdentifier: c.ModelIdentifier, ReasoningEffort: c.ReasoningEffort}})
	}
	created, err := h.leagues.CreateLeague(r.Context(), input)
	if writeError(w, r, err, "league_create_failed", "could not create league", "league_id", request.ID) {
		return
	}
	w.Header().Set("Location", "/v1/leagues/"+created.ID)
	_ = httpapi.WriteJSON(w, http.StatusCreated, leagueResponse(created))
}

func (h *Handler) ListLeagues(w http.ResponseWriter, r *http.Request) {
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			apierror.Write(w, r, http.StatusBadRequest, "invalid limit")
			return
		}
		limit = parsed
	}
	summaries, err := h.leagues.ListLeagues(r.Context(), limit)
	if writeError(w, r, err, "league_list_failed", "could not list leagues") {
		return
	}
	items := make([]summaryJSON, 0, len(summaries))
	for _, s := range summaries {
		item := summaryJSON{ID: s.ID, OwnerID: s.OwnerID, Name: s.Name, Status: s.Status, GroupCount: s.GroupCount, EntrantCount: s.GroupCount * league.GroupSize,
			Progress: progressJSON{Settled: s.Settled, Total: league.TotalFixtures(s.GroupCount)}, CreatedAt: s.CreatedAt, CompletedAt: s.CompletedAt}
		if s.Champion != nil && s.ChampionName != nil {
			item.Champion = &championJSON{Entrant: *s.Champion, FactionName: *s.ChampionName}
		}
		items = append(items, item)
	}
	scheduling, err := h.leagues.Scheduling(r.Context())
	if writeError(w, r, err, "league_list_failed", "could not list leagues") {
		return
	}
	_ = httpapi.WriteJSON(w, http.StatusOK, map[string]any{"leagues": items, "scheduling": schedulingJSON{BattleSlots: scheduling.BattleSlots, CLISlots: scheduling.CLISlots}})
}

func (h *Handler) GetLeague(w http.ResponseWriter, r *http.Request) {
	found, err := h.leagues.GetLeague(r.Context(), r.PathValue("id"))
	if writeError(w, r, err, "league_read_failed", "could not read league", "league_id", r.PathValue("id")) {
		return
	}
	_ = httpapi.WriteJSON(w, http.StatusOK, leagueResponse(found))
}

func (h *Handler) CancelLeague(w http.ResponseWriter, r *http.Request) {
	cancelled, err := h.leagues.CancelLeague(r.Context(), r.PathValue("id"))
	if writeError(w, r, err, "league_cancel_failed", "could not cancel league", "league_id", r.PathValue("id")) {
		return
	}
	_ = httpapi.WriteJSON(w, http.StatusOK, leagueResponse(cancelled))
}

// DisqualifyEntrant removes an entrant, given by its index, from a running league. The
// body may give a reason: {"reason": "..."}.
func (h *Handler) DisqualifyEntrant(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Reason string `json:"reason"`
	}
	entrant, err := strconv.Atoi(r.PathValue("entrant"))
	if err != nil || httpapi.ReadJSON(w, r, &request) != nil {
		apierror.Write(w, r, http.StatusBadRequest, "invalid request")
		return
	}
	updated, err := h.leagues.DisqualifyEntrant(r.Context(), r.PathValue("id"), entrant, request.Reason)
	if writeError(w, r, err, "league_disqualify_failed", "could not disqualify entrant", "league_id", r.PathValue("id"), "entrant", entrant) {
		return
	}
	_ = httpapi.WriteJSON(w, http.StatusOK, leagueResponse(updated))
}

const invalidLeagueMessage = "invalid league"

type errorCase struct {
	errs    []error
	status  int
	code    apierror.ErrorCode
	message string
}

var errorCases = []errorCase{
	{errs: []error{authn.ErrForbidden}, status: http.StatusForbidden, message: "permission denied"},
	{errs: []error{leagueservice.ErrInvalidLeague, battleservice.ErrInvalidBattle}, status: http.StatusBadRequest, message: invalidLeagueMessage},
	{errs: []error{leagueservice.ErrLeagueNotFound}, status: http.StatusNotFound, message: "league not found"},
	{errs: []error{league.ErrEntrantNotFound}, status: http.StatusNotFound, message: "entrant not found"},
	{errs: []error{battleservice.ErrBattleFactionMissing, battleservice.ErrScenarioNotFound}, status: http.StatusNotFound, message: "league dependency not found"},
	{errs: []error{battleservice.ErrIncompatibleFaction, game.ErrInvalidBlueprint, battleservice.ErrModelProfileUnavailable}, status: http.StatusUnprocessableEntity, message: "invalid league configuration or unavailable controller"},
	{errs: []error{leagueservice.ErrLeagueAlreadyExists}, status: http.StatusConflict, code: apierror.CodeAlreadyExists, message: "league already exists"},
	{errs: []error{leagueservice.ErrLeagueNotActive}, status: http.StatusConflict, message: "league is not active"},
	{errs: []error{league.ErrEntrantEliminated}, status: http.StatusConflict, message: "entrant has no match left to play"},
	{errs: []error{league.ErrLastContender}, status: http.StatusConflict, message: "entrant is the last one who can still win; cancel the league instead"},
}

// writeError answers a known error with its status, or logs it and writes a generic 500.
// It reports whether err was non-nil.
func writeError(w http.ResponseWriter, r *http.Request, err error, event, message string, attrs ...any) bool {
	if err == nil {
		return false
	}
	for _, c := range errorCases {
		for _, target := range c.errs {
			if !errors.Is(err, target) {
				continue
			}
			if c.code != "" {
				apierror.WriteCode(w, r, c.status, c.code, c.message)
			} else {
				apierror.Write(w, r, c.status, c.message)
			}
			return true
		}
	}
	slog.ErrorContext(r.Context(), event, append(append([]any{"request_id", httpapi.RequestID(r.Context())}, attrs...), "error", err)...)
	apierror.Write(w, r, http.StatusInternalServerError, message)
	return true
}

func groupIndex(letter string) (int, bool) {
	if len(letter) != 1 || letter[0] < 'A' || letter[0] > 'D' {
		return 0, false
	}
	return int(letter[0] - 'A'), true
}

type schedulingJSON struct {
	BattleSlots int `json:"battleSlots"`
	CLISlots    int `json:"cliSlots"`
}

type summaryJSON struct {
	ID           string        `json:"id"`
	OwnerID      string        `json:"ownerId"`
	Name         string        `json:"name"`
	Status       string        `json:"status"`
	GroupCount   int           `json:"groupCount"`
	EntrantCount int           `json:"entrantCount"`
	Champion     *championJSON `json:"champion"`
	Progress     progressJSON  `json:"progress"`
	CreatedAt    time.Time     `json:"createdAt"`
	CompletedAt  *time.Time    `json:"completedAt"`
}

type championJSON struct {
	Entrant     int    `json:"entrant"`
	FactionName string `json:"factionName"`
}

type progressJSON struct {
	Settled int `json:"settled"`
	Total   int `json:"total"`
}
