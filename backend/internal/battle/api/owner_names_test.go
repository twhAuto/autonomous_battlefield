// Checks that battle and history responses name each battle's owner, for every role,
// with one label lookup per response, and stay readable when the lookup fails.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
)

type fakeOwnerNames struct {
	labels map[string]string
	err    error
	calls  [][]string
}

func (f *fakeOwnerNames) Labels(_ context.Context, ownerIDs []string) (map[string]string, error) {
	f.calls = append(f.calls, ownerIDs)
	return f.labels, f.err
}

func serveAs(handler *Handler, role authn.Role, method, path string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	request := httptest.NewRequest(method, path, nil)
	request = request.WithContext(authn.WithActor(request.Context(), authn.Actor{ID: "viewer", Role: role}))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	return response
}

func TestBattleResponsesNameTheOwnerForEveryRole(t *testing.T) {
	const id = "7f22e693-bacd-46bb-b942-64fa613cd388"
	for _, role := range []authn.Role{authn.Guest, authn.Player, authn.Tester, authn.Admin} {
		for _, route := range []struct{ method, path string }{{"GET", "/v1/battles/" + id}, {"POST", "/v1/battles/" + id + "/stop"}, {"POST", "/v1/battles/" + id + "/pause"}} {
			owners := &fakeOwnerNames{labels: map[string]string{"issuer|ada": "Ada"}}
			service := &fakeBattleService{run: &battleservice.BattleRun{ID: id, OwnerID: "issuer|ada", Status: "running"}}
			response := serveAs(NewHandler(service).WithOwnerNames(owners), role, route.method, route.path)
			var body struct{ OwnerID, OwnerName string }
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != http.StatusOK {
				t.Fatalf("%s %s as %s: %d %s", route.method, route.path, role, response.Code, response.Body)
			}
			if body.OwnerName != "Ada" || len(owners.calls) != 1 || owners.calls[0][0] != "issuer|ada" {
				t.Fatalf("%s %s as %s: owner %q, lookups %v", route.method, route.path, role, body.OwnerName, owners.calls)
			}
		}
	}
}

func TestBattleHistoryNamesOwnersWithOneLookup(t *testing.T) {
	owners := &fakeOwnerNames{labels: map[string]string{"issuer|ada": "Ada", authn.LegacyOwner: "Admin"}}
	service := &fakeBattleService{historyPage: battleservice.BattleHistoryPage{Battles: []battleservice.BattleSummary{
		{ID: "1", OwnerID: "issuer|ada"}, {ID: "2", OwnerID: authn.LegacyOwner}, {ID: "3", OwnerID: "issuer|ada"},
	}}}
	response := serveAs(NewHandler(service).WithOwnerNames(owners), authn.Guest, "GET", "/v1/battles")
	var page struct {
		Battles []map[string]any `json:"battles"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || response.Code != http.StatusOK || len(page.Battles) != 3 {
		t.Fatalf("history: %d %s", response.Code, response.Body)
	}
	for i, want := range []string{"Ada", "Admin", "Ada"} {
		if page.Battles[i]["ownerName"] != want {
			t.Fatalf("battle %d owner = %v, want %s", i, page.Battles[i]["ownerName"], want)
		}
		if _, exposed := page.Battles[i]["ownerId"]; exposed {
			t.Fatalf("history row exposes its owner ID: %v", page.Battles[i])
		}
	}
	if len(owners.calls) != 1 {
		t.Fatalf("history made %d label lookups, want 1", len(owners.calls))
	}
}

func TestBattleReadsOmitOwnerNamesWhenTheLookupFails(t *testing.T) {
	const id = "7f22e693-bacd-46bb-b942-64fa613cd388"
	owners := &fakeOwnerNames{err: errors.New("database down")}
	service := &fakeBattleService{
		run:         &battleservice.BattleRun{ID: id, OwnerID: "issuer|ada", Status: "running"},
		historyPage: battleservice.BattleHistoryPage{Battles: []battleservice.BattleSummary{{ID: "1", OwnerID: "issuer|ada"}}},
	}
	for _, path := range []string{"/v1/battles/" + id, "/v1/battles"} {
		for _, handler := range []*Handler{NewHandler(service).WithOwnerNames(owners), NewHandler(service)} {
			response := serveAs(handler, authn.Player, "GET", path)
			if response.Code != http.StatusOK || strings.Contains(response.Body.String(), `"ownerName"`) {
				t.Fatalf("%s: %d %s", path, response.Code, response.Body)
			}
		}
	}
}
