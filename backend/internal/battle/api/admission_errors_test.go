package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/game"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/apierror"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/authn"
	battleservice "github.com/twhAuto/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/httpapi"
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
			assertBattleDenial(t, route.method, route.path, authn.ErrForbidden, 403, apierror.CodePermissionDenied, "permission denied")
			assertBattleDenial(t, route.method, route.path, battleservice.ErrLimited, 429, apierror.CodeRateLimited, "battle allowance exhausted")
		})
	}
}

func assertBattleDenial(t *testing.T, method, path string, cause error, status int, code apierror.ErrorCode, message string) {
	t.Helper()
	service := &fakeBattleService{err: fmt.Errorf("private database detail: %w", cause)}
	request := httptest.NewRequest(method, path, strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	httpapi.NewRouter(NewHandler(deniedPerspectiveService{service})).ServeHTTP(response, request)
	// The wrapped cause must never reach the client, whatever the body format.
	if strings.Contains(response.Body.String(), "private database detail") {
		t.Fatalf("%s %s: response leaked the underlying cause: %q", method, path, response.Body.String())
	}
	var envelope struct {
		Error apierror.ErrorBody `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("%s %s: response is not a JSON error envelope: %v (%q)", method, path, err, response.Body.String())
	}
	if response.Code != status || envelope.Error.Code != code || envelope.Error.Message != message {
		t.Fatalf("%s %s: got %d %+v, want %d %s %q", method, path, response.Code, envelope.Error, status, code, message)
	}
	if envelope.Error.RequestID == "" {
		t.Fatalf("%s %s: envelope is missing the request ID assigned by the router", method, path)
	}
}

type deniedPerspectiveService struct{ *fakeBattleService }

func (s deniedPerspectiveService) GetFactionView(context.Context, string, string) (game.View, error) {
	return game.View{}, s.err
}
