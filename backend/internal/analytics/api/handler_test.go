// Checks the analytics route's query parsing, response and error statuses.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/analytics"
	analyticsservice "github.com/twh09/autonomous_battlefield/backend/internal/analytics/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
)

type fakeRepo struct {
	filter analyticsservice.Filter
	err    error
}

func (r *fakeRepo) CompletedBattles(_ context.Context, filter analyticsservice.Filter, _ int) ([]analytics.Battle, error) {
	r.filter = filter
	return nil, r.err
}

func serve(t *testing.T, repo *fakeRepo, role authn.Role, target string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	NewHandler(analyticsservice.New(repo, "9")).RegisterRoutes(mux)
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request = request.WithContext(authn.WithActor(request.Context(), authn.Actor{ID: "actor", Role: role}))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	return response
}

func TestGetReportParsesFiltersAndReturnsTheReport(t *testing.T) {
	repo := &fakeRepo{}
	response := serve(t, repo, authn.Tester, "/v1/analytics?rulesVersion=8&since=2026-10-01T00:00:00Z&excludeFallback=true&leagueId=9d7a1d2c-6c43-4cf8-9a6e-0c5a1f3b2e10")
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != http.StatusOK {
		t.Fatalf("status %d body %s", response.Code, response.Body.String())
	}
	if body["rulesVersion"] != "8" || body["battles"] != 0.0 || body["models"] == nil || repo.filter.Since == nil || repo.filter.LeagueID == "" {
		t.Fatalf("body = %v, filter %+v", body, repo.filter)
	}
}

func TestGetReportErrors(t *testing.T) {
	for _, test := range []struct {
		name   string
		role   authn.Role
		target string
		err    error
		status int
	}{
		{"bad since", authn.Tester, "/v1/analytics?since=yesterday", nil, http.StatusBadRequest},
		{"bad fallback flag", authn.Tester, "/v1/analytics?excludeFallback=maybe", nil, http.StatusBadRequest},
		{"bad league", authn.Tester, "/v1/analytics?leagueId=league", nil, http.StatusBadRequest},
		{"service identity", authn.CI, "/v1/analytics", nil, http.StatusForbidden},
		{"storage", authn.Admin, "/v1/analytics", errors.New("database down"), http.StatusInternalServerError},
	} {
		if response := serve(t, &fakeRepo{err: test.err}, test.role, test.target); response.Code != test.status {
			t.Errorf("%s: status %d, want %d", test.name, response.Code, test.status)
		}
	}
}
