package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/faction"
	factionservice "github.com/twh09/autonomous_battlefield/backend/internal/faction/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/httpapi"
)

func TestGetFactionNameReturnsJSON(t *testing.T) {
	factionService := &fakeFactionService{factionName: "Northwind"}
	handler := NewHandler(factionService)
	request := httptest.NewRequest(http.MethodGet, "/v1/factions/7f22e693-bacd-46bb-b942-64fa613cd388/name", nil)
	response := httptest.NewRecorder()

	handler.GetFactionName(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("Content-Type = %q, want %q", contentType, "application/json")
	}

	var body map[string]string
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
	if body["name"] != "Northwind" {
		t.Fatalf("name = %q, want %q", body["name"], "Northwind")
	}
}

func TestCreateFactionRoute(t *testing.T) {
	factionService := &fakeFactionService{}
	router := newFactionTestRouter(factionService)
	request := httptest.NewRequest(http.MethodPost, "/v1/factions", bytes.NewBufferString(
		`{"id":"7f22e693-bacd-46bb-b942-64fa613cd388","name":"Northwind"}`,
	))
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusCreated)
	}
	if factionService.createdFaction == nil || factionService.createdFaction.Name != "Northwind" {
		t.Fatalf("created faction = %#v", factionService.createdFaction)
	}
}

func TestListFactionsReturnsJSON(t *testing.T) {
	factionService := &fakeFactionService{factions: []faction.Faction{
		{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", Name: "Northwind"},
		{ID: "4db8d152-1e04-4d10-8824-859c6ed72878", Name: "Verdant Accord"},
	}}
	router := newFactionTestRouter(factionService)
	request := httptest.NewRequest(http.MethodGet, "/v1/factions", nil)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	var body struct {
		Factions []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"factions"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
	if len(body.Factions) != 2 || body.Factions[0].Name != "Northwind" {
		t.Fatalf("factions = %#v, want two ordered factions", body.Factions)
	}
}

func TestListRulesetsReturnsDesignCatalog(t *testing.T) {
	service := &fakeRulesetService{fakeFactionService: &fakeFactionService{}, rulesets: []game.Ruleset{{ID: "castle-siege-v1", DesignBudget: 120}}}
	router := httpapi.NewRouter(NewHandler(service))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/faction-rulesets", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"designBudget":120`) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}

func TestListFactionsReturnsEmptyArray(t *testing.T) {
	router := newFactionTestRouter(&fakeFactionService{})
	request := httptest.NewRequest(http.MethodGet, "/v1/factions", nil)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if body := response.Body.String(); body != "{\"factions\":[]}" {
		t.Fatalf("body = %q, want an empty JSON array", body)
	}
}

func TestFactionReadsMapServiceErrors(t *testing.T) {
	t.Run("list", func(t *testing.T) {
		response := httptest.NewRecorder()
		NewHandler(&fakeFactionService{listErr: errors.New("database unavailable")}).ListFactions(
			response,
			httptest.NewRequest(http.MethodGet, "/v1/factions", nil),
		)
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
		}
	})

	for _, test := range []struct {
		name   string
		err    error
		status int
	}{
		{name: "missing", err: factionservice.ErrFactionNotFound, status: http.StatusNotFound},
		{name: "internal", err: errors.New("database unavailable"), status: http.StatusInternalServerError},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			NewHandler(&fakeFactionService{getErr: test.err}).GetFactionName(
				response,
				httptest.NewRequest(http.MethodGet, "/v1/factions/id/name", nil),
			)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
		})
	}
}

func TestCreateFactionErrors(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		serviceErr error
		wantStatus int
	}{
		{name: "malformed body", body: `{`, wantStatus: http.StatusBadRequest},
		{name: "trailing JSON", body: `{}` + `{}`, wantStatus: http.StatusBadRequest},
		{name: "invalid faction", body: `{}`, serviceErr: factionservice.ErrInvalidFaction, wantStatus: http.StatusBadRequest},
		{name: "invalid blueprint", body: `{}`, serviceErr: fmt.Errorf("%w: unknown ruleset", factionservice.ErrInvalidBlueprint), wantStatus: http.StatusUnprocessableEntity},
		{name: "duplicate faction", body: `{}`, serviceErr: factionservice.ErrFactionAlreadyExists, wantStatus: http.StatusConflict},
		{name: "repository failure", body: `{}`, serviceErr: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			factionService := &fakeFactionService{createErr: tt.serviceErr}
			handler := NewHandler(factionService)
			request := httptest.NewRequest(http.MethodPost, "/v1/factions", bytes.NewBufferString(tt.body))
			response := httptest.NewRecorder()

			handler.CreateFaction(response, request)

			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, tt.wantStatus)
			}
		})
	}
}

func TestDeleteFactionRouteAndErrors(t *testing.T) {
	id := "7f22e693-bacd-46bb-b942-64fa613cd388"
	service := &fakeFactionService{}
	response := httptest.NewRecorder()
	newFactionTestRouter(service).ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/v1/factions/"+id, nil))
	if response.Code != http.StatusNoContent || service.deletedID != id {
		t.Fatalf("status=%d deleted=%q", response.Code, service.deletedID)
	}

	for _, test := range []struct {
		err    error
		status int
	}{{factionservice.ErrInvalidFaction, http.StatusBadRequest}, {factionservice.ErrFactionNotFound, http.StatusNotFound}, {errors.New("database unavailable"), http.StatusInternalServerError}} {
		service := &fakeFactionService{deleteErr: test.err}
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodDelete, "/v1/factions/"+id, nil)
		request.SetPathValue("id", id)
		NewHandler(service).DeleteFaction(response, request)
		if response.Code != test.status {
			t.Fatalf("error=%v status=%d want=%d", test.err, response.Code, test.status)
		}
	}
}

type fakeFactionService struct {
	createdFaction *factionservice.CreateInput
	createErr      error
	factionName    string
	factions       []faction.Faction
	getErr         error
	listErr        error
	deletedID      string
	deleteErr      error
}

type fakeRulesetService struct {
	*fakeFactionService
	rulesets []game.Ruleset
}

func (s *fakeRulesetService) ListRulesets() []game.Ruleset { return s.rulesets }

func newFactionTestRouter(factions factionservice.Service) http.Handler {
	return httpapi.NewRouter(NewHandler(factions))
}

func (s *fakeFactionService) GetFactionName(context.Context, string) (string, error) {
	return s.factionName, s.getErr
}

func (s *fakeFactionService) ListFactions(context.Context) ([]faction.Faction, error) {
	return s.factions, s.listErr
}

func (s *fakeFactionService) CreateFaction(_ context.Context, input factionservice.CreateInput) error {
	s.createdFaction = &input
	return s.createErr
}

func (s *fakeFactionService) DeleteFaction(_ context.Context, id string) error {
	s.deletedID = id
	return s.deleteErr
}
