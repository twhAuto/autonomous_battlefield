// Maps battle, scenario, profile, event, and control HTTP routes to service operations and
// stable response statuses.

package api

import (
	"errors"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	"log/slog"
	"net/http"
	"strconv"

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
		http.Error(w, permissionDeniedMessage, 403)
		return
	}
	if errors.Is(err, battleservice.ErrLimited) {
		http.Error(w, battleAllowanceMessage, 429)
		return
	}
	if errors.Is(err, battleservice.ErrInvalidBattle) {
		http.Error(w, invalidBattleIDMessage, http.StatusBadRequest)
		return
	}
	if errors.Is(err, battleservice.ErrBattleNotFound) {
		http.Error(w, battleNotFoundMessage, http.StatusNotFound)
		return
	}
	if errors.Is(err, battleservice.ErrBattleNotActive) {
		http.Error(w, "battle is not active", http.StatusConflict)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "battle_control_failed", "request_id", httpapi.RequestID(r.Context()), "battle_id", r.PathValue("id"), "paused", paused, "error", err)
		http.Error(w, "could not update battle control", http.StatusInternalServerError)
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
		http.Error(w, permissionDeniedMessage, 403)
		return
	}
	if errors.Is(err, battleservice.ErrLimited) {
		http.Error(w, battleAllowanceMessage, 429)
		return
	}
	if errors.Is(err, battleservice.ErrInvalidBattle) {
		http.Error(w, invalidBattleIDMessage, http.StatusBadRequest)
		return
	}
	if errors.Is(err, battleservice.ErrBattleNotFound) {
		http.Error(w, battleNotFoundMessage, http.StatusNotFound)
		return
	}
	if errors.Is(err, battleservice.ErrBattleNotActive) {
		http.Error(w, "battle is not active", http.StatusConflict)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "battle_stop_failed", "request_id", httpapi.RequestID(r.Context()), "battle_id", r.PathValue("id"), "error", err)
		http.Error(w, "could not stop battle", http.StatusInternalServerError)
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
		http.Error(w, "invalid battle attempt", http.StatusBadRequest)
		return
	}
	decisions, err := service.ListDecisionHistory(r.Context(), r.PathValue("id"), attempt)
	if errors.Is(err, authn.ErrForbidden) {
		http.Error(w, permissionDeniedMessage, 403)
		return
	}
	if errors.Is(err, battleservice.ErrLimited) {
		http.Error(w, battleAllowanceMessage, 429)
		return
	}
	if errors.Is(err, battleservice.ErrInvalidBattle) {
		http.Error(w, "invalid decision query", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, "could not list decisions", http.StatusInternalServerError)
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
		http.Error(w, "perspective views unavailable", http.StatusNotImplemented)
		return
	}
	view, err := service.GetFactionView(r.Context(), r.PathValue("id"), r.PathValue("factionId"))
	if errors.Is(err, authn.ErrForbidden) {
		http.Error(w, permissionDeniedMessage, 403)
		return
	}
	if errors.Is(err, battleservice.ErrLimited) {
		http.Error(w, battleAllowanceMessage, 429)
		return
	}
	if errors.Is(err, battleservice.ErrInvalidBattle) || errors.Is(err, battleservice.ErrInvalidPerspective) {
		http.Error(w, "invalid battle perspective", http.StatusBadRequest)
		return
	}
	if errors.Is(err, battleservice.ErrBattleNotFound) {
		http.Error(w, battleNotFoundMessage, http.StatusNotFound)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "battle_view_failed", "request_id", httpapi.RequestID(r.Context()), "battle_id", r.PathValue("id"), "faction_id", r.PathValue("factionId"), "error", err)
		http.Error(w, "could not get battle view", http.StatusInternalServerError)
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
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	participants := make([]battleservice.ParticipantInput, len(request.Participants))
	for index, participant := range request.Participants {
		participants[index] = battleservice.ParticipantInput{FactionID: participant.FactionID, Controller: game.ControllerConfig{Type: participant.Controller.Type, ProfileID: participant.Controller.ProfileID}}
	}
	run, err := h.battles.CreateBattle(r.Context(), battleservice.CreateBattleInput{ID: request.ID, ScenarioID: request.ScenarioID, Participants: participants, TickCount: request.TickCount})
	if errors.Is(err, authn.ErrForbidden) {
		http.Error(w, permissionDeniedMessage, 403)
		return
	}
	if errors.Is(err, battleservice.ErrLimited) {
		http.Error(w, battleAllowanceMessage, 429)
		return
	}
	if errors.Is(err, battleservice.ErrInvalidBattle) {
		http.Error(w, "invalid battle", http.StatusBadRequest)
		return
	}
	if errors.Is(err, battleservice.ErrBattleFactionMissing) || errors.Is(err, battleservice.ErrScenarioNotFound) {
		http.Error(w, "battle dependency not found", http.StatusNotFound)
		return
	}
	if errors.Is(err, battleservice.ErrIncompatibleFaction) || errors.Is(err, game.ErrInvalidBlueprint) {
		http.Error(w, "invalid battle configuration or unavailable controller", http.StatusUnprocessableEntity)
		return
	}
	if errors.Is(err, battleservice.ErrModelProfileUnavailable) {
		http.Error(w, "invalid battle configuration or unavailable controller", http.StatusUnprocessableEntity)
		return
	}
	if errors.Is(err, battleservice.ErrBattleAlreadyExists) {
		http.Error(w, "battle already exists", http.StatusConflict)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "battle_enqueue_failed", "request_id", httpapi.RequestID(r.Context()), "battle_id", request.ID, "error", err)
		http.Error(w, "could not create battle", http.StatusInternalServerError)
		return
	}
	if run == nil {
		slog.ErrorContext(r.Context(), "battle_enqueue_failed", "request_id", httpapi.RequestID(r.Context()), "battle_id", request.ID, "error", "battle service returned no run")
		http.Error(w, "could not create battle", http.StatusInternalServerError)
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
		http.Error(w, permissionDeniedMessage, 403)
		return
	}
	if errors.Is(err, battleservice.ErrLimited) {
		http.Error(w, battleAllowanceMessage, 429)
		return
	}
	if errors.Is(err, battleservice.ErrInvalidBattle) {
		http.Error(w, "invalid battle ID", http.StatusBadRequest)
		return
	}
	if errors.Is(err, battleservice.ErrBattleNotFound) {
		http.Error(w, battleNotFoundMessage, http.StatusNotFound)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "battle_status_read_failed", "request_id", httpapi.RequestID(r.Context()), "battle_id", r.PathValue("id"), "error", err)
		http.Error(w, "could not get battle", http.StatusInternalServerError)
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
		http.Error(w, permissionDeniedMessage, 403)
		return
	}
	if errors.Is(err, battleservice.ErrLimited) {
		http.Error(w, battleAllowanceMessage, 429)
		return
	}
	if errors.Is(err, battleservice.ErrInvalidBattle) {
		http.Error(w, "invalid event query", http.StatusBadRequest)
		return
	}
	if errors.Is(err, battleservice.ErrBattleNotFound) {
		http.Error(w, battleNotFoundMessage, http.StatusNotFound)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "battle_events_read_failed", "request_id", httpapi.RequestID(r.Context()), "battle_id", r.PathValue("id"), "error", err)
		http.Error(w, "could not list battle events", http.StatusInternalServerError)
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
			http.Error(w, "invalid event limit", http.StatusBadRequest)
			return 0, 0, nil, false, false
		}
		limit = parsed
	}
	var after uint64
	if raw := query.Get("after"); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			http.Error(w, "invalid event cursor", http.StatusBadRequest)
			return 0, 0, nil, false, false
		}
		after = parsed
	}
	var attempt *int
	if raw := query.Get("attempt"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			http.Error(w, "invalid battle attempt", http.StatusBadRequest)
			return 0, 0, nil, false, false
		}
		attempt = &parsed
	}
	latest := false
	if raw := query.Get("latest"); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			http.Error(w, "invalid latest-events flag", http.StatusBadRequest)
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
