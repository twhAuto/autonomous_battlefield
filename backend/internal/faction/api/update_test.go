// Checks the faction-edit HTTP route and its response behavior.

package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/authn"
	factionservice "github.com/twhAuto/autonomous_battlefield/backend/internal/faction/service"
)

func TestUpdateFactionRoute(t *testing.T) {
	for _, test := range []struct {
		body   string
		err    error
		status int
	}{
		{`{"name":"A","doctrine":"Scout"}`, nil, http.StatusNoContent},
		{`{`, nil, http.StatusBadRequest},
		{`{"name":"A","doctrine":"Scout","blueprint":{}}`, nil, http.StatusBadRequest},
		{`{}`, factionservice.ErrInvalidFaction, http.StatusBadRequest},
		{`{}`, authn.ErrForbidden, http.StatusForbidden},
		{`{}`, factionservice.ErrFactionNotFound, http.StatusNotFound},
		{`{}`, errors.New("offline"), http.StatusInternalServerError},
	} {
		s := &fakeFactionService{updateErr: test.err}
		mux := http.NewServeMux()
		NewHandler(s).RegisterRoutes(mux)
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/v1/factions/7f22e693-bacd-46bb-b942-64fa613cd388", strings.NewReader(test.body)))
		if response.Code != test.status {
			t.Fatalf("body=%s status=%d", test.body, response.Code)
		}
		if test.status == http.StatusNoContent && (s.updatedFaction.Doctrine != "Scout" || s.updatedFaction.ID != "7f22e693-bacd-46bb-b942-64fa613cd388") {
			t.Fatalf("input=%+v", s.updatedFaction)
		}
	}
}
