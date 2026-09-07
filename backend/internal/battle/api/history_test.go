// Checks battle-history HTTP filters, pagination, and service error responses.

package api

import (
	"github.com/twh09/autonomous_battlefield/backend/internal/testutil"

	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
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
			testutil.Require(t, !(response.Code != test.status || service.historyInput.Limit != test.limit), func() { t.Fatalf("code=%d body=%s input=%+v", response.Code, response.Body, service.historyInput) })
			if test.status == 200 {
				var page battleservice.BattleHistoryPage
				{
					err := json.Unmarshal(response.Body.Bytes(), &page)
					testutil.Require(t, !(err != nil || page.Battles == nil || page.NextCursor != nil), func() { t.Fatalf("body=%s err=%v", response.Body, err) })
				}
			}
			testutil.Require(t, !(test.name == "filter and cursor" && (service.historyInput.Status != "paused" || service.historyInput.Cursor != "abc")), func() { t.Fatal(service.historyInput) })
		})
	}
}
