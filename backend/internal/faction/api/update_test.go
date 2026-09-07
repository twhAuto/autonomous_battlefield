// Checks the faction-edit HTTP route and its response behavior.

package api

import (
	"context"
	"errors"
	factionservice "github.com/twh09/autonomous_battlefield/backend/internal/faction/service"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type updateService struct {
	*fakeFactionService
	input factionservice.CreateInput
	err   error
}

func (s *updateService) UpdateFaction(_ context.Context, input factionservice.CreateInput) error {
	s.input = input
	return s.err
}
func TestUpdateFactionRoute(t *testing.T) {
	for _, test := range []struct {
		body   string
		err    error
		status int
	}{
		{`{"name":"A","doctrine":"Scout"}`, nil, 204},
		{`{`, nil, 400},
		{`{"name":"A","doctrine":"Scout","blueprint":{}}`, nil, 400},
		{`{}`, factionservice.ErrInvalidFaction, 400},
		{`{}`, factionservice.ErrFactionNotFound, 404},
		{`{}`, errors.New("offline"), 500},
	} {
		s := &updateService{fakeFactionService: &fakeFactionService{}, err: test.err}
		mux := http.NewServeMux()
		NewHandler(s).RegisterRoutes(mux)
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest("PUT", "/v1/factions/7f22e693-bacd-46bb-b942-64fa613cd388", strings.NewReader(test.body)))
		if response.Code != test.status {
			t.Fatalf("body=%s status=%d", test.body, response.Code)
		}
		if test.status == 204 && (s.input.Doctrine != "Scout" || s.input.ID != "7f22e693-bacd-46bb-b942-64fa613cd388") {
			t.Fatalf("input=%+v", s.input)
		}
	}
	response := httptest.NewRecorder()
	NewHandler(&fakeFactionService{}).UpdateFaction(response, httptest.NewRequest("PUT", "/", strings.NewReader(`{}`)))
	if response.Code != 500 {
		t.Fatal(response.Code)
	}
}
