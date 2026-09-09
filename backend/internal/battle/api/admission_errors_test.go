package api

import (
	"context"
	"fmt"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/httpapi"
)

// Clients must receive stable denial statuses without underlying database details.
func TestBattleAdmissionAndPermissionErrors(t *testing.T) {
	for _, route := range []struct{ method, path string }{
		{"POST", "/v1/battles"},
		{"GET", "/v1/battles/battle"},
		{"POST", "/v1/battles/battle/stop"},
		{"POST", "/v1/battles/battle/pause"},
		{"POST", "/v1/battles/battle/resume"},
		{"GET", "/v1/battles/battle/events"},
		{"GET", "/v1/battles/battle/decisions?attempt=1"},
		{"GET", "/v1/battles/battle/factions/one/view"},
	} {
		t.Run(route.method+route.path, func(t *testing.T) {
			assertBattleDenial(t, route.method, route.path, authn.ErrForbidden, 403, "permission denied")
			assertBattleDenial(t, route.method, route.path, battleservice.ErrLimited, 429, "battle allowance exhausted")
		})
	}
}

func assertBattleDenial(t *testing.T, method, path string, cause error, status int, message string) {
	t.Helper()
	service := &fakeBattleService{err: fmt.Errorf("private database detail: %w", cause)}
	request := httptest.NewRequest(method, path, strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	httpapi.NewRouter(NewHandler(deniedPerspectiveService{service})).ServeHTTP(response, request)
	if response.Code != status || strings.TrimSpace(response.Body.String()) != message {
		t.Fatalf("%s %s: got %d %q, want %d %q", method, path, response.Code, response.Body.String(), status, message)
	}
}

type deniedPerspectiveService struct{ *fakeBattleService }

func (s deniedPerspectiveService) GetFactionView(context.Context, string, string) (game.View, error) {
	return game.View{}, s.err
}
