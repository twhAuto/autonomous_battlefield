// Checks battle-history HTTP filters, pagination, service error responses, and that
// every reader sees each side's controller.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
)

func TestListBattleHistoryHTTP(t *testing.T) {
	for _, test := range []struct {
		name, query   string
		err           error
		status, limit int
	}{
		{"default", "", nil, 200, 25},
		{"filter and cursor", "?status=paused&cursor=abc&limit=1", nil, 200, 1},
		{"malformed limit", "?limit=no", nil, 400, 0},
		{"empty limit", "?limit=", nil, 400, 0},
		{"invalid query", "?status=other", battleservice.ErrInvalidBattle, 400, 25},
		{"database failure", "", errors.New("private database details"), 500, 25},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeBattleService{err: test.err, historyPage: battleservice.BattleHistoryPage{Battles: []battleservice.BattleSummary{}}}
			mux := http.NewServeMux()
			NewHandler(service).RegisterRoutes(mux)
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequest("GET", "/v1/battles"+test.query, nil))
			if response.Code != test.status || service.historyInput.Limit != test.limit {
				t.Fatalf("code=%d body=%s input=%+v", response.Code, response.Body, service.historyInput)
			}
			if test.status == 200 {
				checkEmptyHistoryPage(t, response)
			}
			if test.name == "filter and cursor" && (service.historyInput.Status != "paused" || service.historyInput.Cursor != "abc") {
				t.Fatal(service.historyInput)
			}
		})
	}
}

// checkEmptyHistoryPage requires a last page that encodes its empty list as [] rather
// than null.
func checkEmptyHistoryPage(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	var page battleservice.BattleHistoryPage
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || page.Battles == nil || page.NextCursor != nil {
		t.Fatalf("body=%s err=%v", response.Body, err)
	}
}

func TestBattleHistoryNamesEachSidesModelForGuests(t *testing.T) {
	model := game.ControllerConfig{Type: "model", ProfileID: "frontier", ModelIdentifier: "gpt-test", ReasoningEffort: "high"}
	service := &fakeBattleService{historyPage: battleservice.BattleHistoryPage{Battles: []battleservice.BattleSummary{{ID: "battle", Participants: []battleservice.BattleParticipantSummary{{ID: "a", Name: "A", Controller: model}}}}}}
	mux := http.NewServeMux()
	NewHandler(service).RegisterRoutes(mux)
	response := httptest.NewRecorder()
	guest := authn.WithActor(context.Background(), authn.Actor{ID: "guest", Role: authn.Guest})
	mux.ServeHTTP(response, httptest.NewRequest("GET", "/v1/battles", nil).WithContext(guest))
	var page battleservice.BattleHistoryPage
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || len(page.Battles) != 1 || page.Battles[0].Participants[0].Controller != model {
		t.Fatalf("code=%d body=%s err=%v", response.Code, response.Body, err)
	}
}
