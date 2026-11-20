// Checks that the faction list names each faction's owner with one lookup and stays
// readable when the lookup fails.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/authn"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/faction"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/httpapi"
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

func listFactionsAs(t *testing.T, handler *Handler, role authn.Role) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/v1/factions", nil)
	request = request.WithContext(authn.WithActor(request.Context(), authn.Actor{ID: "viewer", Role: role}))
	response := httptest.NewRecorder()
	httpapi.NewRouter(handler).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", response.Code, response.Body)
	}
	return response
}

func TestListFactionsNamesEachOwner(t *testing.T) {
	factions := &fakeFactionService{factions: []faction.Faction{
		{ID: "1", Name: "Northwind", OwnerID: "issuer|ada"},
		{ID: "2", Name: "Verdant Accord", OwnerID: authn.LegacyOwner},
		{ID: "3", Name: "Ashen Host", OwnerID: "issuer|grace"},
	}}
	for _, role := range []authn.Role{authn.Guest, authn.Player, authn.Admin} {
		owners := &fakeOwnerNames{labels: map[string]string{"issuer|ada": "Ada", authn.LegacyOwner: "Admin", "issuer|grace": "Unnamed commander"}}
		response := listFactionsAs(t, NewHandler(factions).WithOwnerNames(owners), role)
		var body struct {
			Factions []struct{ ID, Name, OwnerID, OwnerName string } `json:"factions"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || len(body.Factions) != 3 {
			t.Fatalf("as %s: %s", role, response.Body)
		}
		for i, want := range []string{"Ada", "Admin", "Unnamed commander"} {
			if got := body.Factions[i]; got.OwnerName != want || got.Name != factions.factions[i].Name || got.OwnerID != factions.factions[i].OwnerID {
				t.Fatalf("as %s, faction %d = %+v, want owner %s", role, i, got, want)
			}
		}
		if len(owners.calls) != 1 || len(owners.calls[0]) != 3 {
			t.Fatalf("as %s, lookups = %v", role, owners.calls)
		}
	}
}

func TestListFactionsOmitsOwnerNamesWhenUnavailable(t *testing.T) {
	factions := &fakeFactionService{factions: []faction.Faction{{ID: "1", Name: "Northwind", OwnerID: "issuer|ada"}}}
	for _, handler := range []*Handler{NewHandler(factions).WithOwnerNames(&fakeOwnerNames{err: errors.New("database down")}), NewHandler(factions)} {
		if body := listFactionsAs(t, handler, authn.Player).Body.String(); strings.Contains(body, "ownerName") || !strings.Contains(body, "Northwind") {
			t.Fatalf("body = %s", body)
		}
	}
	owners := &fakeOwnerNames{}
	if body := listFactionsAs(t, NewHandler(&fakeFactionService{}).WithOwnerNames(owners), authn.Player).Body.String(); body != `{"factions":[]}` || len(owners.calls) != 0 {
		t.Fatalf("empty registry: %s after %d lookups", body, len(owners.calls))
	}
}
