// Checks that the session reports the caller's display name from the People hook,
// never asks it about Guests or service identities, and fails rather than guessing
// when the name cannot be read.

package authn

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type sessionBody struct {
	Actor struct {
		ID          string  `json:"id"`
		Role        Role    `json:"role"`
		DisplayName *string `json:"displayName"`
	} `json:"actor"`
	Error struct{ Code string } `json:"error"`
}

func (f *managedPlayerFixture) session(jwt string) (int, sessionBody) {
	f.t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/v1/auth/session", nil)
	request.Header.Set("Cf-Access-Jwt-Assertion", jwt)
	response := httptest.NewRecorder()
	NewRoutes(f.manager).session(response, request)
	var body sessionBody
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		f.t.Fatal("invalid session response", response.Body.String())
	}
	return response.Code, body
}

func TestSessionReportsTheCallersDisplayName(t *testing.T) {
	f := newGuestFixture(t, "guest-app")
	asked := map[string]Role{}
	f.manager.config.People = func(_ context.Context, actor Actor) (string, error) {
		asked[actor.ID] = actor.Role
		if actor.Role == Player {
			return "Iron Duke", nil
		}
		return "", nil
	}
	status, body := f.session(f.token("friend@example.com", "game", "friend"))
	if status != http.StatusOK || body.Actor.DisplayName == nil || *body.Actor.DisplayName != "Iron Duke" {
		t.Fatalf("player session: %d %+v", status, body)
	}
	if status, body = f.session(f.token("owner@example.com", "game", "admin")); status != http.StatusOK || body.Actor.DisplayName != nil {
		t.Fatalf("an unnamed admin must omit displayName: %d %+v", status, body)
	}
	if status, body = f.session(f.token("visitor@example.com", "guest-app", "visitor")); status != http.StatusOK || body.Actor.Role != Guest || body.Actor.DisplayName != nil {
		t.Fatalf("guest session: %d %+v", status, body)
	}
	want := map[string]Role{f.cfg.Issuer + "|friend": Player, f.cfg.Issuer + "|admin": Admin}
	if len(asked) != len(want) || asked[f.cfg.Issuer+"|friend"] != Player || asked[f.cfg.Issuer+"|admin"] != Admin {
		t.Fatalf("People was asked about %v, want %v", asked, want)
	}
	if _, err := NewRoutes(f.manager).sessionActor(context.Background(), Actor{ID: "service", Role: CI}); err != nil || len(asked) != len(want) {
		t.Fatal("People was asked about a service identity")
	}
}

func TestSessionFailsWhenTheDisplayNameCannotBeRead(t *testing.T) {
	f := newManagedPlayerFixture(t)
	f.manager.config.People = func(context.Context, Actor) (string, error) { return "", errors.New("database down") }
	status, body := f.session(f.token("friend@example.com", "game", "friend"))
	if status != http.StatusServiceUnavailable || body.Error.Code != "service_unavailable" {
		t.Fatalf("session: %d %+v", status, body)
	}
}

func TestLocalDevelopmentSessionRecordsTheLocalAdmin(t *testing.T) {
	var recorded Actor
	manager := NewManager(Config{People: func(_ context.Context, actor Actor) (string, error) { recorded = actor; return "", nil }})
	response := httptest.NewRecorder()
	NewRoutes(manager).session(response, httptest.NewRequest(http.MethodGet, "/v1/auth/session", nil))
	if response.Code != http.StatusOK || recorded.ID != localDevelopmentActor || recorded.Role != Admin {
		t.Fatalf("session %d recorded %+v", response.Code, recorded)
	}
}
