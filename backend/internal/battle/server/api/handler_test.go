package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/server/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/httpapi"
)

func TestCreateBattleReturnsAccepted(t *testing.T) {
	battles := &fakeBattleService{run: &battleservice.BattleRun{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", Status: "queued"}}
	router := httpapi.NewRouter(NewHandler(battles))
	request := httptest.NewRequest(http.MethodPost, "/v1/battles", bytes.NewBufferString(`{
		"id":"7f22e693-bacd-46bb-b942-64fa613cd388",
		"scenarioId":"skirmish-v1",
		"factionIds":["4db8d152-1e04-4d10-8824-859c6ed72878","41ac3a2f-3371-4de1-b0f6-70600fcd9052"],
		"tickCount":100
	}`))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusAccepted)
	}
	if response.Header().Get("Location") != "/v1/battles/7f22e693-bacd-46bb-b942-64fa613cd388" {
		t.Fatalf("Location = %q", response.Header().Get("Location"))
	}
	if battles.createInput.TickCount != 100 || battles.createInput.ScenarioID != "skirmish-v1" {
		t.Fatalf("create input = %#v", battles.createInput)
	}
}

func TestListScenariosReturnsCatalog(t *testing.T) {
	battles := &fakeBattleService{scenarios: []battleservice.ScenarioSummary{{
		ID: "skirmish-v1", RulesVersion: "1", TickDuration: "0s", DecisionIntervalTicks: 10, DecisionLeadTicks: 2,
	}}}
	router := httpapi.NewRouter(NewHandler(battles))
	request := httptest.NewRequest(http.MethodGet, "/v1/scenarios", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	var body struct {
		Scenarios []battleservice.ScenarioSummary `json:"scenarios"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Scenarios) != 1 || body.Scenarios[0].ID != "skirmish-v1" {
		t.Fatalf("scenarios = %#v", body.Scenarios)
	}
}

func TestGetBattleReturnsStatusAndParticipants(t *testing.T) {
	battles := &fakeBattleService{run: &battleservice.BattleRun{
		ID: "7f22e693-bacd-46bb-b942-64fa613cd388", ScenarioID: "skirmish-v1", Status: "running", Attempt: 1,
		ScenarioSnapshot: game.Scenario{Factions: []game.FactionSetup{{ID: "faction-a", Name: "A"}, {ID: "faction-b", Name: "B"}}},
	}}
	handler := NewHandler(battles)
	request := httptest.NewRequest(http.MethodGet, "/v1/battles/7f22e693-bacd-46bb-b942-64fa613cd388", nil)
	request.SetPathValue("id", "7f22e693-bacd-46bb-b942-64fa613cd388")
	response := httptest.NewRecorder()
	handler.GetBattle(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	var body struct {
		Status       string              `json:"status"`
		Participants []map[string]string `json:"participants"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Status != "running" || len(body.Participants) != 2 {
		t.Fatalf("body = %#v", body)
	}
}

func TestListBattleEventsParsesCursor(t *testing.T) {
	cursor := uint64(9)
	battles := &fakeBattleService{page: battleservice.BattleEventPage{
		Attempt: 2, NextCursor: &cursor,
		Events: []battleservice.StoredBattleEvent{{Attempt: 2, Sequence: 9, Tick: 3, Type: "tick_completed", CreatedAt: time.Now()}},
	}}
	handler := NewHandler(battles)
	request := httptest.NewRequest(http.MethodGet, "/v1/battles/id/events?attempt=2&limit=25&latest=true", nil)
	request.SetPathValue("id", "7f22e693-bacd-46bb-b942-64fa613cd388")
	response := httptest.NewRecorder()
	handler.ListEvents(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if battles.eventsAttempt == nil || *battles.eventsAttempt != 2 || battles.eventsAfter != 0 || battles.eventsLimit != 25 || !battles.eventsLatest {
		t.Fatalf("event arguments = attempt %v, after %d, limit %d, latest %t", battles.eventsAttempt, battles.eventsAfter, battles.eventsLimit, battles.eventsLatest)
	}
}

func TestBattleHandlerMapsErrors(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
	}{
		{name: "invalid", err: battleservice.ErrInvalidBattle, status: http.StatusBadRequest},
		{name: "missing", err: battleservice.ErrBattleFactionMissing, status: http.StatusNotFound},
		{name: "incompatible", err: battleservice.ErrIncompatibleFaction, status: http.StatusUnprocessableEntity},
		{name: "duplicate", err: battleservice.ErrBattleAlreadyExists, status: http.StatusConflict},
		{name: "internal", err: errors.New("database unavailable"), status: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewHandler(&fakeBattleService{err: tt.err})
			request := httptest.NewRequest(http.MethodPost, "/v1/battles", bytes.NewBufferString(`{}`))
			response := httptest.NewRecorder()
			handler.CreateBattle(response, request)
			if response.Code != tt.status {
				t.Fatalf("status = %d, want %d", response.Code, tt.status)
			}
		})
	}
}

func TestCreateBattleRejectsMalformedBody(t *testing.T) {
	handler := NewHandler(&fakeBattleService{})
	response := httptest.NewRecorder()
	handler.CreateBattle(response, httptest.NewRequest(http.MethodPost, "/v1/battles", bytes.NewBufferString("{")))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestGetBattleMapsMalformedID(t *testing.T) {
	handler := NewHandler(&fakeBattleService{err: battleservice.ErrInvalidBattle})
	request := httptest.NewRequest(http.MethodGet, "/v1/battles/not-a-uuid", nil)
	request.SetPathValue("id", "not-a-uuid")
	response := httptest.NewRecorder()
	handler.GetBattle(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestGetBattleMapsReadErrors(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		status int
	}{
		{name: "missing", err: battleservice.ErrBattleNotFound, status: http.StatusNotFound},
		{name: "internal", err: errors.New("database unavailable"), status: http.StatusInternalServerError},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := NewHandler(&fakeBattleService{err: test.err})
			request := httptest.NewRequest(http.MethodGet, "/v1/battles/id", nil)
			request.SetPathValue("id", "id")
			response := httptest.NewRecorder()
			handler.GetBattle(response, request)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
		})
	}
}

func TestListEventsRejectsInvalidQueryParameters(t *testing.T) {
	for _, query := range []string{"limit=many", "after=-1", "attempt=many", "latest=sometimes"} {
		t.Run(query, func(t *testing.T) {
			handler := NewHandler(&fakeBattleService{})
			request := httptest.NewRequest(http.MethodGet, "/v1/battles/id/events?"+query, nil)
			request.SetPathValue("id", "id")
			response := httptest.NewRecorder()
			handler.ListEvents(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestListEventsMapsServiceErrorsAndDefaults(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		status int
	}{
		{name: "invalid", err: battleservice.ErrInvalidBattle, status: http.StatusBadRequest},
		{name: "missing", err: battleservice.ErrBattleNotFound, status: http.StatusNotFound},
		{name: "internal", err: errors.New("database unavailable"), status: http.StatusInternalServerError},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeBattleService{err: test.err}
			handler := NewHandler(service)
			request := httptest.NewRequest(http.MethodGet, "/v1/battles/id/events", nil)
			request.SetPathValue("id", "id")
			response := httptest.NewRecorder()
			handler.ListEvents(response, request)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
			if service.eventsLimit != 100 || service.eventsAfter != 0 || service.eventsAttempt != nil {
				t.Fatalf("defaults = attempt %v, after %d, limit %d", service.eventsAttempt, service.eventsAfter, service.eventsLimit)
			}
		})
	}
}

type fakeBattleService struct {
	run           *battleservice.BattleRun
	page          battleservice.BattleEventPage
	err           error
	createInput   battleservice.CreateBattleInput
	eventsAttempt *int
	eventsAfter   uint64
	eventsLimit   int
	eventsLatest  bool
	scenarios     []battleservice.ScenarioSummary
}

func (s *fakeBattleService) ListScenarios() []battleservice.ScenarioSummary { return s.scenarios }

func (s *fakeBattleService) CreateBattle(_ context.Context, input battleservice.CreateBattleInput) (*battleservice.BattleRun, error) {
	s.createInput = input
	return s.run, s.err
}

func (s *fakeBattleService) GetBattle(context.Context, string) (*battleservice.BattleRun, error) {
	return s.run, s.err
}

func (s *fakeBattleService) ListBattleEvents(_ context.Context, _ string, attempt *int, after uint64, limit int, latest bool) (battleservice.BattleEventPage, error) {
	s.eventsAttempt = attempt
	s.eventsAfter = after
	s.eventsLimit = limit
	s.eventsLatest = latest
	return s.page, s.err
}
