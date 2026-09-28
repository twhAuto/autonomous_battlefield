// Maps battle, scenario, profile, event, and control HTTP routes to service operations and
// stable response statuses.

package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/apierror"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"

	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/httpapi"
)

const (
	battleNotFoundMessage  = "battle not found"
	invalidBattleIDMessage = "invalid battle ID"
)

type Handler struct {
	battles battleservice.Service
}

const permissionDeniedMessage = "permission denied"

const battleAllowanceMessage = "battle allowance exhausted"

func NewHandler(battles battleservice.Service) *Handler {
	return &Handler{battles: battles}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/scenarios", h.ListScenarios)
	mux.HandleFunc("GET /v1/model-profiles", h.ListModelProfiles)
	mux.HandleFunc("POST /v1/battles", h.CreateBattle)
	mux.HandleFunc("GET /v1/battles", h.ListBattles)
	mux.HandleFunc("GET /v1/battles/{id}", h.GetBattle)
	mux.HandleFunc("POST /v1/battles/{id}/stop", h.StopBattle)
	mux.HandleFunc("POST /v1/battles/{id}/pause", h.PauseBattle)
	mux.HandleFunc("POST /v1/battles/{id}/resume", h.ResumeBattle)
	mux.HandleFunc("GET /v1/battles/{id}/factions/{factionId}/view", h.GetFactionView)
	mux.HandleFunc("GET /v1/battles/{id}/events", h.ListEvents)
	mux.HandleFunc("GET /v1/battles/{id}/decisions", h.ListDecisions)
	mux.HandleFunc("GET /v1/battles/{id}/decisions/{requestId}/context", h.GetDecisionContext)
}

func (h *Handler) PauseBattle(w http.ResponseWriter, r *http.Request) {
	h.setBattlePaused(w, r, true)
}

func (h *Handler) ResumeBattle(w http.ResponseWriter, r *http.Request) {
	h.setBattlePaused(w, r, false)
}

func (h *Handler) setBattlePaused(w http.ResponseWriter, r *http.Request, paused bool) {
	var run *battleservice.BattleRun
	var err error
	if paused {
		run, err = h.battles.PauseBattle(r.Context(), r.PathValue("id"))
	} else {
		run, err = h.battles.ResumeBattle(r.Context(), r.PathValue("id"))
	}
	if errors.Is(err, authn.ErrForbidden) {
		apierror.Write(w, r, 403, permissionDeniedMessage)
		return
	}
	if errors.Is(err, battleservice.ErrLimited) {
		apierror.Write(w, r, 429, battleAllowanceMessage)
		return
	}
	if errors.Is(err, battleservice.ErrInvalidBattle) {
		apierror.Write(w, r, http.StatusBadRequest, invalidBattleIDMessage)
		return
	}
	if errors.Is(err, battleservice.ErrBattleNotFound) {
		apierror.Write(w, r, http.StatusNotFound, battleNotFoundMessage)
		return
	}
	if errors.Is(err, battleservice.ErrBattleNotActive) {
		apierror.Write(w, r, http.StatusConflict, "battle is not active")
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "battle_control_failed", "request_id", httpapi.RequestID(r.Context()), "battle_id", r.PathValue("id"), "paused", paused, "error", err)
		apierror.Write(w, r, http.StatusInternalServerError, "could not update battle control")
		return
	}
	event := "battle_resumed"
	if paused {
		event = "battle_paused"
	}
	slog.InfoContext(r.Context(), event, "request_id", httpapi.RequestID(r.Context()), "battle_id", run.ID, "attempt", run.Attempt, "current_tick", run.CurrentTick)
	_ = httpapi.WriteJSON(w, http.StatusOK, battleResponseFor(r, run))
}

func (h *Handler) StopBattle(w http.ResponseWriter, r *http.Request) {
	run, err := h.battles.StopBattle(r.Context(), r.PathValue("id"))
	if errors.Is(err, authn.ErrForbidden) {
		apierror.Write(w, r, 403, permissionDeniedMessage)
		return
	}
	if errors.Is(err, battleservice.ErrLimited) {
		apierror.Write(w, r, 429, battleAllowanceMessage)
		return
	}
	if errors.Is(err, battleservice.ErrInvalidBattle) {
		apierror.Write(w, r, http.StatusBadRequest, invalidBattleIDMessage)
		return
	}
	if errors.Is(err, battleservice.ErrBattleNotFound) {
		apierror.Write(w, r, http.StatusNotFound, battleNotFoundMessage)
		return
	}
	if errors.Is(err, battleservice.ErrBattleNotActive) {
		apierror.Write(w, r, http.StatusConflict, "battle is not active")
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "battle_stop_failed", "request_id", httpapi.RequestID(r.Context()), "battle_id", r.PathValue("id"), "error", err)
		apierror.Write(w, r, http.StatusInternalServerError, "could not stop battle")
		return
	}
	slog.InfoContext(r.Context(), "battle_stopped", "request_id", httpapi.RequestID(r.Context()), "battle_id", run.ID, "attempt", run.Attempt, "current_tick", run.CurrentTick)
	_ = httpapi.WriteJSON(w, http.StatusOK, battleResponseFor(r, run))
}

func (h *Handler) ListDecisions(w http.ResponseWriter, r *http.Request) {
	service, ok := h.battles.(battleservice.DecisionHistoryService)
	if !ok {
		_ = httpapi.WriteJSON(w, http.StatusOK, map[string]any{"decisions": []any{}})
		return
	}
	attempt, err := strconv.Atoi(r.URL.Query().Get("attempt"))
	if err != nil {
		apierror.Write(w, r, http.StatusBadRequest, "invalid battle attempt")
		return
	}
	decisions, err := service.ListDecisionHistory(r.Context(), r.PathValue("id"), attempt)
	if errors.Is(err, authn.ErrForbidden) {
		apierror.Write(w, r, 403, permissionDeniedMessage)
		return
	}
	if errors.Is(err, battleservice.ErrLimited) {
		apierror.Write(w, r, 429, battleAllowanceMessage)
		return
	}
	if errors.Is(err, battleservice.ErrInvalidBattle) {
		apierror.Write(w, r, http.StatusBadRequest, "invalid decision query")
		return
	}
	if err != nil {
		apierror.Write(w, r, http.StatusInternalServerError, "could not list decisions")
		return
	}
	_ = httpapi.WriteJSON(w, http.StatusOK, map[string]any{"decisions": decisions})
}

func (h *Handler) ListModelProfiles(w http.ResponseWriter, r *http.Request) {
	service, ok := h.battles.(battleservice.ModelProfileService)
	if !ok {
		_ = httpapi.WriteJSON(w, http.StatusOK, map[string]any{"profiles": []any{}})
		return
	}
	_ = httpapi.WriteJSON(w, http.StatusOK, map[string]any{"profiles": visibleProfiles(r, service.ListModelProfiles())})
}

func (h *Handler) GetFactionView(w http.ResponseWriter, r *http.Request) {
	service, ok := h.battles.(battleservice.PerspectiveService)
	if !ok {
		apierror.Write(w, r, http.StatusNotImplemented, "perspective views unavailable")
		return
	}
	view, err := service.GetFactionView(r.Context(), r.PathValue("id"), r.PathValue("factionId"))
	if errors.Is(err, authn.ErrForbidden) {
		apierror.Write(w, r, 403, permissionDeniedMessage)
		return
	}
	if errors.Is(err, battleservice.ErrLimited) {
		apierror.Write(w, r, 429, battleAllowanceMessage)
		return
	}
	if errors.Is(err, battleservice.ErrInvalidBattle) || errors.Is(err, battleservice.ErrInvalidPerspective) {
		apierror.Write(w, r, http.StatusBadRequest, "invalid battle perspective")
		return
	}
	if errors.Is(err, battleservice.ErrBattleNotFound) {
		apierror.Write(w, r, http.StatusNotFound, battleNotFoundMessage)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "battle_view_failed", "request_id", httpapi.RequestID(r.Context()), "battle_id", r.PathValue("id"), "faction_id", r.PathValue("factionId"), "error", err)
		apierror.Write(w, r, http.StatusInternalServerError, "could not get battle view")
		return
	}
	_ = httpapi.WriteJSON(w, http.StatusOK, view)
}

func (h *Handler) ListScenarios(w http.ResponseWriter, _ *http.Request) {
	_ = httpapi.WriteJSON(w, http.StatusOK, map[string]any{"scenarios": h.battles.ListScenarios()})
}

func (h *Handler) CreateBattle(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ID           string `json:"id"`
		ScenarioID   string `json:"scenarioId"`
		Participants []struct {
			FactionID  string `json:"factionId"`
			Controller struct {
				Type      string `json:"type"`
				ProfileID string `json:"profileId,omitempty"`
			} `json:"controller"`
		} `json:"participants"`
		TickCount uint64 `json:"tickCount"`
	}
	if err := httpapi.ReadJSON(w, r, &request); err != nil {
		apierror.Write(w, r, http.StatusBadRequest, "invalid request body")
		return
	}
	participants := make([]battleservice.ParticipantInput, len(request.Participants))
	for index, participant := range request.Participants {
		participants[index] = battleservice.ParticipantInput{FactionID: participant.FactionID, Controller: game.ControllerConfig{Type: participant.Controller.Type, ProfileID: participant.Controller.ProfileID}}
	}
	run, err := h.battles.CreateBattle(r.Context(), battleservice.CreateBattleInput{ID: request.ID, ScenarioID: request.ScenarioID, Participants: participants, TickCount: request.TickCount})
	if errors.Is(err, authn.ErrForbidden) {
		apierror.Write(w, r, 403, permissionDeniedMessage)
		return
	}
	if errors.Is(err, battleservice.ErrLimited) {
		apierror.Write(w, r, 429, battleAllowanceMessage)
		return
	}
	if errors.Is(err, battleservice.ErrInvalidBattle) {
		apierror.Write(w, r, http.StatusBadRequest, "invalid battle")
		return
	}
	if errors.Is(err, battleservice.ErrBattleFactionMissing) || errors.Is(err, battleservice.ErrScenarioNotFound) {
		apierror.Write(w, r, http.StatusNotFound, "battle dependency not found")
		return
	}
	if errors.Is(err, battleservice.ErrIncompatibleFaction) || errors.Is(err, game.ErrInvalidBlueprint) {
		apierror.Write(w, r, http.StatusUnprocessableEntity, "invalid battle configuration or unavailable controller")
		return
	}
	if errors.Is(err, battleservice.ErrModelProfileUnavailable) {
		apierror.Write(w, r, http.StatusUnprocessableEntity, "invalid battle configuration or unavailable controller")
		return
	}
	if errors.Is(err, battleservice.ErrBattleAlreadyExists) {
		apierror.WriteCode(w, r, http.StatusConflict, apierror.CodeAlreadyExists, "battle already exists")
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "battle_enqueue_failed", "request_id", httpapi.RequestID(r.Context()), "battle_id", request.ID, "error", err)
		apierror.Write(w, r, http.StatusInternalServerError, "could not create battle")
		return
	}
	if run == nil {
		slog.ErrorContext(r.Context(), "battle_enqueue_failed", "request_id", httpapi.RequestID(r.Context()), "battle_id", request.ID, "error", "battle service returned no run")
		apierror.Write(w, r, http.StatusInternalServerError, "could not create battle")
		return
	}
	w.Header().Set("Location", "/v1/battles/"+run.ID)
	slog.InfoContext(r.Context(), "battle_enqueued",
		"request_id", httpapi.RequestID(r.Context()),
		"battle_id", run.ID,
		"scenario_id", request.ScenarioID,
		"requested_ticks", request.TickCount,
		"participant_count", len(request.Participants),
	)
	_ = httpapi.WriteJSON(w, http.StatusAccepted, map[string]any{"id": run.ID, "status": run.Status})
}

func (h *Handler) GetBattle(w http.ResponseWriter, r *http.Request) {
	run, err := h.battles.GetBattle(r.Context(), r.PathValue("id"))
	if errors.Is(err, authn.ErrForbidden) {
		apierror.Write(w, r, 403, permissionDeniedMessage)
		return
	}
	if errors.Is(err, battleservice.ErrLimited) {
		apierror.Write(w, r, 429, battleAllowanceMessage)
		return
	}
	if errors.Is(err, battleservice.ErrInvalidBattle) {
		apierror.Write(w, r, http.StatusBadRequest, "invalid battle ID")
		return
	}
	if errors.Is(err, battleservice.ErrBattleNotFound) {
		apierror.Write(w, r, http.StatusNotFound, battleNotFoundMessage)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "battle_status_read_failed", "request_id", httpapi.RequestID(r.Context()), "battle_id", r.PathValue("id"), "error", err)
		apierror.Write(w, r, http.StatusInternalServerError, "could not get battle")
		return
	}
	_ = httpapi.WriteJSON(w, http.StatusOK, battleResponseFor(r, run))
}

func (h *Handler) ListEvents(w http.ResponseWriter, r *http.Request) {
	limit, after, attempt, latest, ok := parseEventQuery(w, r)
	if !ok {
		return
	}

	page, err := h.battles.ListBattleEvents(r.Context(), r.PathValue("id"), attempt, after, limit, latest)
	if errors.Is(err, authn.ErrForbidden) {
		apierror.Write(w, r, 403, permissionDeniedMessage)
		return
	}
	if errors.Is(err, battleservice.ErrLimited) {
		apierror.Write(w, r, 429, battleAllowanceMessage)
		return
	}
	if errors.Is(err, battleservice.ErrInvalidBattle) {
		apierror.Write(w, r, http.StatusBadRequest, "invalid event query")
		return
	}
	if errors.Is(err, battleservice.ErrBattleNotFound) {
		apierror.Write(w, r, http.StatusNotFound, battleNotFoundMessage)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "battle_events_read_failed", "request_id", httpapi.RequestID(r.Context()), "battle_id", r.PathValue("id"), "error", err)
		apierror.Write(w, r, http.StatusInternalServerError, "could not list battle events")
		return
	}
	if authn.IsPlayer(r.Context()) {
		for i := range page.Events {
			page.Events[i].Data = map[string]any{}
		}
	}
	_ = httpapi.WriteJSON(w, http.StatusOK, page)
}

func parseEventQuery(w http.ResponseWriter, r *http.Request) (int, uint64, *int, bool, bool) {
	query := r.URL.Query()
	limit := 100
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			apierror.Write(w, r, http.StatusBadRequest, "invalid event limit")
			return 0, 0, nil, false, false
		}
		limit = parsed
	}
	var after uint64
	if raw := query.Get("after"); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			apierror.Write(w, r, http.StatusBadRequest, "invalid event cursor")
			return 0, 0, nil, false, false
		}
		after = parsed
	}
	var attempt *int
	if raw := query.Get("attempt"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			apierror.Write(w, r, http.StatusBadRequest, "invalid battle attempt")
			return 0, 0, nil, false, false
		}
		attempt = &parsed
	}
	latest := false
	if raw := query.Get("latest"); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			apierror.Write(w, r, http.StatusBadRequest, "invalid latest-events flag")
			return 0, 0, nil, false, false
		}
		latest = parsed
	}

	return limit, after, attempt, latest, true
}

func battleResponse(run *battleservice.BattleRun) any {
	participants := make([]map[string]any, 0, len(run.ScenarioSnapshot.Factions))
	for _, faction := range run.ScenarioSnapshot.Factions {
		participants = append(participants, map[string]any{"id": faction.ID, "name": faction.Name, "controller": faction.Controller, "background": faction.Background, "doctrine": faction.Doctrine})
	}
	return struct {
		OwnerID        string  `json:"ownerId"`
		ID             string  `json:"id"`
		ScenarioID     string  `json:"scenarioId"`
		ScenarioDigest string  `json:"scenarioDigest"`
		RequestedTicks uint64  `json:"requestedTicks"`
		CurrentTick    uint64  `json:"currentTick"`
		Status         string  `json:"status"`
		Paused         bool    `json:"paused"`
		Attempt        int     `json:"attempt"`
		Participants   any     `json:"participants"`
		Result         any     `json:"result"`
		Map            any     `json:"map"`
		State          any     `json:"state"`
		ErrorCode      *string `json:"errorCode,omitempty"`
		ErrorMessage   *string `json:"errorMessage,omitempty"`
	}{
		OwnerID: run.OwnerID, ID: run.ID, ScenarioID: run.ScenarioID, ScenarioDigest: run.ScenarioDigest,
		RequestedTicks: run.RequestedTicks, CurrentTick: run.CurrentTick, Status: run.Status, Paused: run.Paused,
		Attempt: run.Attempt, Participants: participants, Result: run.Result,
		Map: run.ScenarioSnapshot.Map, State: run.CurrentState,
		ErrorCode: run.ErrorCode, ErrorMessage: run.ErrorMessage,
	}
}

func visibleProfiles(r *http.Request, profiles []ai.ModelProfile) any {
	if authn.IsPlayer(r.Context()) {
		result := []map[string]any{}
		for _, p := range profiles {
			if ai.ProfileAllowed(r.Context(), p) && p.Available {
				result = append(result, map[string]any{"id": p.ID, "displayName": p.DisplayName, "modelLabel": p.ModelLabel, "runtime": p.Runtime, "available": true})
			}
		}
		return result
	}
	if _, ok := authn.ActorFromContext(r.Context()); !ok {
		return profiles
	}
	result := []ai.ModelProfile{}
	for _, p := range profiles {
		if ai.ProfileAllowed(r.Context(), p) {
			result = append(result, p)
		}
	}
	return result
}

func battleResponseFor(r *http.Request, run *battleservice.BattleRun) any {
	if !authn.IsPlayer(r.Context()) {
		return battleResponse(run)
	}
	participants := []map[string]any{}
	for _, f := range run.ScenarioSnapshot.Factions {
		participants = append(participants, map[string]any{"id": f.ID, "name": f.Name, "background": f.Background, "doctrine": f.Doctrine, "controller": map[string]string{"type": f.Controller.Type}})
	}
	response := map[string]any{"id": run.ID, "ownerId": run.OwnerID, "scenarioId": run.ScenarioID, "requestedTicks": run.RequestedTicks, "currentTick": run.CurrentTick, "status": run.Status, "paused": run.Paused, "attempt": run.Attempt, "participants": participants, "result": run.Result, "map": run.ScenarioSnapshot.Map, "state": run.CurrentState}
	if run.Status == "failed" {
		response["errorMessage"] = "The battle could not complete."
	}
	return response
}
