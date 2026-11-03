// Maps battle, scenario, profile, event, and control HTTP routes to service operations and
// stable response statuses.

package api

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/profile"
	"github.com/twh09/autonomous_battlefield/backend/internal/apierror"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"

	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/httpapi"
	"github.com/twh09/autonomous_battlefield/backend/internal/league"
)

const (
	battleNotFoundMessage  = "battle not found"
	invalidBattleIDMessage = "invalid battle ID"
)

type Handler struct {
	battles battleservice.Service
	owners  OwnerNames
}

// OwnerNames labels the people who own battles. Without one, responses omit ownerName.
type OwnerNames interface {
	Labels(ctx context.Context, ownerIDs []string) (map[string]string, error)
}

const permissionDeniedMessage = "permission denied"

const battleAllowanceMessage = "battle allowance exhausted"

func NewHandler(battles battleservice.Service) *Handler {
	return &Handler{battles: battles}
}

// WithOwnerNames labels each battle's owner in battle and history responses.
func (h *Handler) WithOwnerNames(owners OwnerNames) *Handler {
	h.owners = owners
	return h
}

// ownerNames labels the owners in one lookup. Labels are resolved per response, never
// stored with a run, because runs can come from the shared result cache. A failed
// lookup leaves the names out rather than failing the read.
func (h *Handler) ownerNames(r *http.Request, ownerIDs []string) map[string]string {
	if h.owners == nil || len(ownerIDs) == 0 {
		return nil
	}
	labels, err := h.owners.Labels(r.Context(), ownerIDs)
	if err != nil {
		slog.WarnContext(r.Context(), "owner_names_failed", "request_id", httpapi.RequestID(r.Context()), "error", err)
		return nil
	}
	return labels
}

// battleBody is the battle response for the caller, labelled with its owner.
func (h *Handler) battleBody(r *http.Request, run *battleservice.BattleRun) any {
	return battleResponseFor(r, run, h.ownerNames(r, []string{run.OwnerID})[run.OwnerID])
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/scenarios", h.ListScenarios)
	mux.HandleFunc("GET /v1/model-profiles", h.ListModelProfiles)
	mux.HandleFunc("POST /v1/battles", h.CreateBattle)
	mux.HandleFunc("GET /v1/battles", h.ListBattles)
	mux.HandleFunc("GET /v1/battles/allowance", h.GetAllowance)
	mux.HandleFunc("GET /v1/battles/rules-versions", h.ListRulesVersions)
	mux.HandleFunc("GET /v1/battles/{id}", h.GetBattle)
	mux.HandleFunc("POST /v1/battles/{id}/stop", h.StopBattle)
	mux.HandleFunc("POST /v1/battles/{id}/pause", h.PauseBattle)
	mux.HandleFunc("POST /v1/battles/{id}/resume", h.ResumeBattle)
	mux.HandleFunc("GET /v1/battles/{id}/factions/{factionId}/view", h.GetFactionView)
	mux.HandleFunc("GET /v1/battles/{id}/events", h.ListEvents)
	mux.HandleFunc("GET /v1/battles/{id}/replay", h.GetBattleReplay)
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
	if writeError(w, r, err, []errorCase{forbiddenError, limitedError, invalidError(invalidBattleIDMessage), notFoundError, notActiveError},
		failure{"battle_control_failed", "could not update battle control", []any{"battle_id", r.PathValue("id"), "paused", paused}}) {
		return
	}
	event := "battle_resumed"
	if paused {
		event = "battle_paused"
	}
	slog.InfoContext(r.Context(), event, "request_id", httpapi.RequestID(r.Context()), "battle_id", run.ID, "attempt", run.Attempt, "current_tick", run.CurrentTick)
	_ = httpapi.WriteJSON(w, http.StatusOK, h.battleBody(r, run))
}

func (h *Handler) StopBattle(w http.ResponseWriter, r *http.Request) {
	run, err := h.battles.StopBattle(r.Context(), r.PathValue("id"))
	if writeError(w, r, err, []errorCase{forbiddenError, limitedError, invalidError(invalidBattleIDMessage), notFoundError, notActiveError},
		failure{"battle_stop_failed", "could not stop battle", []any{"battle_id", r.PathValue("id")}}) {
		return
	}
	slog.InfoContext(r.Context(), "battle_stopped", "request_id", httpapi.RequestID(r.Context()), "battle_id", run.ID, "attempt", run.Attempt, "current_tick", run.CurrentTick)
	_ = httpapi.WriteJSON(w, http.StatusOK, h.battleBody(r, run))
}

func (h *Handler) ListDecisions(w http.ResponseWriter, r *http.Request) {
	attempt, err := strconv.Atoi(r.URL.Query().Get("attempt"))
	if err != nil {
		apierror.Write(w, r, http.StatusBadRequest, "invalid battle attempt")
		return
	}
	decisions, err := h.battles.ListDecisionHistory(r.Context(), r.PathValue("id"), attempt)
	if writeError(w, r, err, []errorCase{forbiddenError, limitedError, invalidError("invalid decision query")},
		failure{message: "could not list decisions"}) {
		return
	}
	if authn.GameplayOnly(r.Context()) {
		_ = httpapi.WriteJSON(w, http.StatusOK, map[string]any{"decisions": playerDecisions(decisions)})
		return
	}
	_ = httpapi.WriteJSON(w, http.StatusOK, map[string]any{"decisions": decisions})
}

// playerDecision is what a Player may see of a decision: its status and the applied
// plan, without model, usage, timing, trace or error diagnostics.
type playerDecision struct {
	RequestID      string     `json:"requestId"`
	FactionID      string     `json:"factionId"`
	TargetTick     uint64     `json:"targetTick"`
	Status         string     `json:"status"`
	ControllerType string     `json:"controllerType"`
	ResolvedPlan   any        `json:"resolvedPlan,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
	ResolvedAt     *time.Time `json:"resolvedAt,omitempty"`
}

func playerDecisions(decisions []battleservice.DecisionHistory) []playerDecision {
	result := make([]playerDecision, 0, len(decisions))
	for _, d := range decisions {
		result = append(result, playerDecision{RequestID: d.RequestID, FactionID: d.FactionID, TargetTick: d.TargetTick, Status: d.Status, ControllerType: d.ControllerType, ResolvedPlan: d.ResolvedPlan, CreatedAt: d.CreatedAt, ResolvedAt: d.ResolvedAt})
	}
	return result
}

func (h *Handler) ListModelProfiles(w http.ResponseWriter, r *http.Request) {
	_ = httpapi.WriteJSON(w, http.StatusOK, map[string]any{"profiles": visibleProfiles(r, h.battles.ListModelProfiles())})
}

func (h *Handler) GetFactionView(w http.ResponseWriter, r *http.Request) {
	view, err := h.battles.GetFactionView(r.Context(), r.PathValue("id"), r.PathValue("factionId"))
	if writeError(w, r, err, []errorCase{forbiddenError, limitedError, invalidError("invalid battle perspective", battleservice.ErrInvalidPerspective), notFoundError},
		failure{"battle_view_failed", "could not get battle view", []any{"battle_id", r.PathValue("id"), "faction_id", r.PathValue("factionId")}}) {
		return
	}
	_ = httpapi.WriteJSON(w, http.StatusOK, view)
}

// GetAllowance reports the caller's battle allowance before a battle is started.
func (h *Handler) GetAllowance(w http.ResponseWriter, r *http.Request) {
	allowance, err := h.battles.BattleAllowance(r.Context())
	if writeError(w, r, err, nil, failure{event: "battle_allowance_failed", message: "could not load battle allowance"}) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	_ = httpapi.WriteJSON(w, http.StatusOK, allowance)
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
				Type            string `json:"type"`
				ProfileID       string `json:"profileId,omitempty"`
				ModelIdentifier string `json:"modelIdentifier,omitempty"`
				ReasoningEffort string `json:"reasoningEffort,omitempty"`
			} `json:"controller"`
		} `json:"participants"`
		TickCount             uint64  `json:"tickCount"`
		TickDuration          *string `json:"tickDuration,omitempty"`
		DecisionIntervalTicks *uint64 `json:"decisionIntervalTicks,omitempty"`
	}
	if err := httpapi.ReadJSON(w, r, &request); err != nil {
		apierror.Write(w, r, http.StatusBadRequest, "invalid request body")
		return
	}
	participants := make([]battleservice.ParticipantInput, len(request.Participants))
	for index, participant := range request.Participants {
		participants[index] = battleservice.ParticipantInput{FactionID: participant.FactionID, Controller: game.ControllerConfig{Type: participant.Controller.Type, ProfileID: participant.Controller.ProfileID, ModelIdentifier: participant.Controller.ModelIdentifier, ReasoningEffort: participant.Controller.ReasoningEffort}}
	}
	run, err := h.battles.CreateBattle(r.Context(), battleservice.CreateBattleInput{ID: request.ID, ScenarioID: request.ScenarioID, Participants: participants, TickCount: request.TickCount, TickDuration: request.TickDuration, DecisionIntervalTicks: request.DecisionIntervalTicks})
	if writeError(w, r, err, []errorCase{
		forbiddenError, limitedError, invalidError("invalid battle"),
		errorFor(http.StatusNotFound, "battle dependency not found", battleservice.ErrBattleFactionMissing, battleservice.ErrScenarioNotFound),
		errorFor(http.StatusUnprocessableEntity, "invalid battle configuration or unavailable controller", battleservice.ErrIncompatibleFaction, game.ErrInvalidBlueprint, battleservice.ErrModelProfileUnavailable),
		{errs: []error{battleservice.ErrBattleAlreadyExists}, status: http.StatusConflict, code: apierror.CodeAlreadyExists, message: "battle already exists"},
	}, failure{"battle_enqueue_failed", "could not create battle", []any{"battle_id", request.ID}}) {
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
	if writeError(w, r, err, []errorCase{forbiddenError, limitedError, invalidError(invalidBattleIDMessage), notFoundError},
		failure{"battle_status_read_failed", "could not get battle", []any{"battle_id", r.PathValue("id")}}) {
		return
	}
	_ = httpapi.WriteJSON(w, http.StatusOK, h.battleBody(r, run))
}

func (h *Handler) ListEvents(w http.ResponseWriter, r *http.Request) {
	query, ok := parseEventQuery(w, r)
	if !ok {
		return
	}
	page, err := h.battles.ListBattleEvents(r.Context(), r.PathValue("id"), query.attempt, query.after, query.limit, query.latest)
	if writeError(w, r, err, []errorCase{forbiddenError, limitedError, invalidError("invalid event query"), notFoundError},
		failure{"battle_events_read_failed", "could not list battle events", []any{"battle_id", r.PathValue("id")}}) {
		return
	}
	if authn.GameplayOnly(r.Context()) {
		for i := range page.Events {
			page.Events[i].Data = map[string]any{}
		}
	}
	_ = httpapi.WriteJSON(w, http.StatusOK, page)
}

// GetBattleReplay returns the replay segment containing ?tick (default 0). Players
// receive the same frames with empty event payloads, as on the events route.
func (h *Handler) GetBattleReplay(w http.ResponseWriter, r *http.Request) {
	var tick uint64
	if raw := r.URL.Query().Get("tick"); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			apierror.Write(w, r, http.StatusBadRequest, "invalid replay tick")
			return
		}
		tick = parsed
	}
	replay, err := h.battles.GetBattleReplay(r.Context(), r.PathValue("id"), tick)
	if writeError(w, r, err, []errorCase{forbiddenError, limitedError, invalidError("invalid replay query"), notFoundError, replayUnavailableError},
		failure{"battle_replay_failed", "could not get battle replay", []any{"battle_id", r.PathValue("id"), "tick", tick}}) {
		return
	}
	if authn.GameplayOnly(r.Context()) {
		replay = withoutEventPayloads(replay)
	}
	_ = httpapi.WriteJSON(w, http.StatusOK, replay)
}

// withoutEventPayloads copies the frames so the service's segment is never mutated.
func withoutEventPayloads(replay battleservice.BattleReplay) battleservice.BattleReplay {
	frames := make([]battleservice.ReplayFrame, len(replay.Frames))
	for i, frame := range replay.Frames {
		events := make([]game.Event, len(frame.Events))
		for j, event := range frame.Events {
			event.Data = map[string]any{}
			events[j] = event
		}
		frame.Events = events
		frames[i] = frame
	}
	replay.Frames = frames
	return replay
}

// eventQuery holds the parsed ListEvents query parameters.
type eventQuery struct {
	limit   int
	after   uint64
	attempt *int
	latest  bool
}

// parseEventQuery writes a 400 and reports false when a parameter is malformed.
func parseEventQuery(w http.ResponseWriter, r *http.Request) (eventQuery, bool) {
	query := r.URL.Query()
	limit := 100
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			apierror.Write(w, r, http.StatusBadRequest, "invalid event limit")
			return eventQuery{}, false
		}
		limit = parsed
	}
	var after uint64
	if raw := query.Get("after"); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			apierror.Write(w, r, http.StatusBadRequest, "invalid event cursor")
			return eventQuery{}, false
		}
		after = parsed
	}
	var attempt *int
	if raw := query.Get("attempt"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			apierror.Write(w, r, http.StatusBadRequest, "invalid battle attempt")
			return eventQuery{}, false
		}
		attempt = &parsed
	}
	latest := false
	if raw := query.Get("latest"); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			apierror.Write(w, r, http.StatusBadRequest, "invalid latest-events flag")
			return eventQuery{}, false
		}
		latest = parsed
	}

	return eventQuery{limit: limit, after: after, attempt: attempt, latest: latest}, true
}

func battleResponse(run *battleservice.BattleRun, ownerName string) any {
	participants := make([]map[string]any, 0, len(run.ScenarioSnapshot.Factions))
	for _, faction := range run.ScenarioSnapshot.Factions {
		participants = append(participants, map[string]any{"id": faction.ID, "name": faction.Name, "controller": faction.Controller, "background": faction.Background, "doctrine": faction.Doctrine})
	}
	return struct {
		OwnerID        string  `json:"ownerId"`
		OwnerName      string  `json:"ownerName,omitempty"`
		ID             string  `json:"id"`
		ScenarioID     string  `json:"scenarioId"`
		ScenarioDigest string  `json:"scenarioDigest"`
		RequestedTicks uint64  `json:"requestedTicks"`
		CurrentTick    uint64  `json:"currentTick"`
		MaximumUnits   int     `json:"maximumUnits,omitempty"`
		Contested      bool    `json:"contestedTerritory,omitempty"`
		Production     any     `json:"production"`
		Status         string  `json:"status"`
		Paused         bool    `json:"paused"`
		Attempt        int     `json:"attempt"`
		Participants   any     `json:"participants"`
		Result         any     `json:"result"`
		Map            any     `json:"map"`
		State          any     `json:"state"`
		ErrorCode      *string `json:"errorCode,omitempty"`
		ErrorMessage   *string `json:"errorMessage,omitempty"`
		League         any     `json:"league,omitempty"`
	}{
		OwnerID: run.OwnerID, OwnerName: ownerName, ID: run.ID, ScenarioID: run.ScenarioID, ScenarioDigest: run.ScenarioDigest,
		RequestedTicks: run.RequestedTicks, CurrentTick: run.CurrentTick, Status: run.Status, Paused: run.Paused,
		MaximumUnits: run.ScenarioSnapshot.Ruleset.MaximumUnitCount, Contested: run.ScenarioSnapshot.Ruleset.Buildings.ContestedTerritory, Production: productionFor(run),
		Attempt: run.Attempt, Participants: participants, Result: run.Result,
		Map: run.ScenarioSnapshot.Map, State: run.CurrentState,
		ErrorCode: run.ErrorCode, ErrorMessage: run.ErrorMessage,
		League: leagueFor(run),
	}
}

// leagueFor keeps the field absent, not null, for battles outside a league, and names a
// group fixture's match.
func leagueFor(run *battleservice.BattleRun) any {
	if run.League == nil {
		return nil
	}
	link := *run.League
	link.Match = league.MatchName(link.Stage, link.Round, link.Tie)
	return link
}

// visibleProfiles lists the profiles the caller's role may use, each with only the
// models that role may use; Players and Guests see only available profiles.
func visibleProfiles(r *http.Request, profiles []ai.ModelProfile) any {
	if _, ok := authn.ActorFromContext(r.Context()); !ok {
		return profiles
	}
	gameplayOnly := authn.GameplayOnly(r.Context())
	selectable := []ai.ModelProfile{}
	for _, p := range profiles {
		if p, ok := profile.ForRole(r.Context(), p); ok && (p.Available || !gameplayOnly) {
			selectable = append(selectable, p)
		}
	}
	if !gameplayOnly {
		return selectable
	}
	result := make([]map[string]any, 0, len(selectable))
	for _, p := range selectable {
		result = append(result, map[string]any{"id": p.ID, "displayName": p.DisplayName, "modelLabel": p.ModelLabel, "runtime": p.Runtime, "available": true, "modelOptions": p.ModelOptions})
	}
	return result
}

// productionFor reports the next spending point of an active battle, or nil when the
// battle is settled or no window remains before its tick limit.
func productionFor(run *battleservice.BattleRun) *game.ProductionSchedule {
	if run.Status != "queued" && run.Status != "running" {
		return nil
	}
	schedule, ok := game.NextProduction(run.ScenarioSnapshot, run.CurrentState, run.RequestedTicks)
	if !ok {
		return nil
	}
	return &schedule
}

func battleResponseFor(r *http.Request, run *battleservice.BattleRun, ownerName string) any {
	if !authn.GameplayOnly(r.Context()) {
		return battleResponse(run, ownerName)
	}
	participants := []map[string]any{}
	for _, f := range run.ScenarioSnapshot.Factions {
		participants = append(participants, map[string]any{"id": f.ID, "name": f.Name, "background": f.Background, "doctrine": f.Doctrine, "controller": f.Controller})
	}
	response := map[string]any{"id": run.ID, "ownerId": run.OwnerID, "scenarioId": run.ScenarioID, "requestedTicks": run.RequestedTicks, "currentTick": run.CurrentTick, "production": productionFor(run), "status": run.Status, "paused": run.Paused, "attempt": run.Attempt, "participants": participants, "result": run.Result, "map": run.ScenarioSnapshot.Map, "state": run.CurrentState}
	if ownerName != "" {
		response["ownerName"] = ownerName
	}
	if limit := run.ScenarioSnapshot.Ruleset.MaximumUnitCount; limit > 0 {
		response["maximumUnits"] = limit
	}
	if run.ScenarioSnapshot.Ruleset.Buildings.ContestedTerritory {
		response["contestedTerritory"] = true
	}
	if run.Status == "failed" {
		response["errorMessage"] = "The battle could not complete."
	}
	if run.League != nil {
		response["league"] = leagueFor(run)
	}
	return response
}
