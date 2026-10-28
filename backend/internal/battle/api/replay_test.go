// Checks the replay route parses its tick, hides event payloads from Players without
// mutating the segment, and maps replay failures to stable statuses.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/apierror"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
)

const replayRouteBattleID = "7f22e693-bacd-46bb-b942-64fa613cd388"

func replayRequest(t *testing.T, battles *fakeBattleService, query string, role authn.Role) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	NewHandler(battles).RegisterRoutes(mux)
	request := httptest.NewRequest(http.MethodGet, "/v1/battles/"+replayRouteBattleID+"/replay"+query, nil)
	request = request.WithContext(authn.WithActor(context.Background(), authn.Actor{ID: "viewer", Role: role}))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	return response
}

func TestBattleReplayRouteHidesEventPayloadsFromPlayers(t *testing.T) {
	next := uint64(10)
	segment := battleservice.BattleReplay{BattleID: replayRouteBattleID, Attempt: 1, FinalTick: 60, ToTick: 9, NextTick: &next, Frames: []battleservice.ReplayFrame{
		{Tick: 0, State: game.State{BattleID: replayRouteBattleID}, Events: []game.Event{{Sequence: 1, Type: "plan_applied", Data: map[string]any{"name": "secret-plan"}}}},
	}}
	for role, wantPayload := range map[authn.Role]bool{authn.Player: false, authn.Developer: true, authn.Admin: true} {
		battles := &fakeBattleService{replay: segment}
		response := replayRequest(t, battles, "?tick=7", role)
		if response.Code != http.StatusOK || battles.replayTick != 7 {
			t.Fatalf("%s: status=%d tick=%d body=%s", role, response.Code, battles.replayTick, response.Body.String())
		}
		var body battleservice.BattleReplay
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if got := body.Frames[0].Events[0].Data["name"] == "secret-plan"; got != wantPayload || body.NextTick == nil || *body.NextTick != 10 {
			t.Fatalf("%s: payload=%v body=%s", role, got, response.Body.String())
		}
		if segment.Frames[0].Events[0].Data["name"] != "secret-plan" {
			t.Fatal("player projection mutated the service segment")
		}
	}
}

func TestBattleReplayRouteDefaultsAndValidatesTick(t *testing.T) {
	battles := &fakeBattleService{replayTick: 99}
	if response := replayRequest(t, battles, "", authn.Developer); response.Code != http.StatusOK || battles.replayTick != 0 {
		t.Fatalf("default tick: status=%d tick=%d", response.Code, battles.replayTick)
	}
	for _, query := range []string{"?tick=-1", "?tick=soon"} {
		if response := replayRequest(t, &fakeBattleService{}, query, authn.Developer); response.Code != http.StatusBadRequest {
			t.Fatalf("%s: status=%d", query, response.Code)
		}
	}
}

func TestBattleReplayRouteMapsErrors(t *testing.T) {
	for _, test := range []struct {
		err    error
		status int
		code   apierror.ErrorCode
	}{
		{battleservice.ErrReplayUnavailable, http.StatusConflict, apierror.CodeConflict},
		{battleservice.ErrBattleNotFound, http.StatusNotFound, apierror.CodeNotFound},
		{battleservice.ErrInvalidBattle, http.StatusBadRequest, apierror.CodeInvalidRequest},
		{authn.ErrForbidden, http.StatusForbidden, apierror.CodePermissionDenied},
		{errors.New("restore failed"), http.StatusInternalServerError, apierror.CodeInternal},
	} {
		response := replayRequest(t, &fakeBattleService{err: test.err}, "?tick=3", authn.Developer)
		if response.Code != test.status || envelopeOf(t, response).Code != test.code {
			t.Fatalf("%v: status=%d body=%s", test.err, response.Code, response.Body.String())
		}
	}
}
