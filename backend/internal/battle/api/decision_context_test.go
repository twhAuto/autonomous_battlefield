package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/httpapi"
)

type contextService struct {
	*fakeBattleService
	err  error
	args []any
}

func (s *contextService) GetDecisionContext(_ context.Context, battleID, requestID string, attempt int) (battleservice.SavedDecisionContext, error) {
	s.args = []any{battleID, requestID, attempt}
	return battleservice.SavedDecisionContext{RequestID: requestID, BattleID: battleID, Attempt: attempt}, s.err
}

func TestContextRoute(t *testing.T) {
	for _, tc := range []struct {
		query  string
		err    error
		status int
	}{
		{"2", nil, 200}, {"", nil, 400}, {"bad", nil, 400}, {"0", nil, 400},
		{"2", battleservice.ErrInvalidBattle, 400}, {"2", ai.ErrDecisionNotFound, 404}, {"2", errors.New("private DB error"), 500},
	} {
		t.Run(tc.query+http.StatusText(tc.status), func(t *testing.T) {
			service := &contextService{fakeBattleService: &fakeBattleService{}, err: tc.err}
			response := httptest.NewRecorder()
			httpapi.NewRouter(NewHandler(service)).ServeHTTP(response, httptest.NewRequest("GET", "/v1/battles/battle/decisions/request/context?attempt="+tc.query, nil))
			if response.Code != tc.status {
				t.Fatalf("%d: %s", response.Code, response.Body.String())
			}
			if tc.status == 200 && (service.args[0] != "battle" || service.args[1] != "request" || service.args[2] != 2) {
				t.Fatal(service.args)
			}
			if strings.Contains(response.Body.String(), "private DB error") {
				t.Fatal("leaked error")
			}
		})
	}
	response := httptest.NewRecorder()
	httpapi.NewRouter(NewHandler(&fakeBattleService{})).ServeHTTP(response, httptest.NewRequest("GET", "/v1/battles/battle/decisions/request/context?attempt=2", nil))
	if response.Code != 404 {
		t.Fatal(response.Code)
	}
}

func TestParticipantProfileUsesBattleSnapshot(t *testing.T) {
	run := &battleservice.BattleRun{ScenarioSnapshot: game.Scenario{Factions: []game.FactionSetup{{ID: "a", Name: "Historical name", Background: "Historical background", Doctrine: "Historical doctrine"}, {ID: "legacy", Name: "Legacy"}}}}
	data, err := json.Marshal(battleResponse(run))
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Participants []struct{ Background, Doctrine, Name string }
	}
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatal(err)
	}
	if response.Participants[0].Background != "Historical background" || response.Participants[0].Doctrine != "Historical doctrine" || response.Participants[0].Name != "Historical name" || response.Participants[1].Background != "" {
		t.Fatalf("%s", data)
	}
}
