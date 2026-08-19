package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/service/entity"
)

func TestGetFactionNameReturnsJSON(t *testing.T) {
	factionService := &fakeFactionService{factionName: "Northwind"}
	handler := NewFactionHandler(factionService)
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
	router := NewRouter(NewFactionHandler(factionService))
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

func TestCreateFactionErrors(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		serviceErr error
		wantStatus int
	}{
		{name: "malformed body", body: `{`, wantStatus: http.StatusBadRequest},
		{name: "trailing JSON", body: `{}` + `{}`, wantStatus: http.StatusBadRequest},
		{name: "invalid faction", body: `{}`, serviceErr: service.ErrInvalidFaction, wantStatus: http.StatusBadRequest},
		{name: "duplicate faction", body: `{}`, serviceErr: service.ErrFactionAlreadyExists, wantStatus: http.StatusConflict},
		{name: "repository failure", body: `{}`, serviceErr: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			factionService := &fakeFactionService{createErr: tt.serviceErr}
			handler := NewFactionHandler(factionService)
			request := httptest.NewRequest(http.MethodPost, "/v1/factions", bytes.NewBufferString(tt.body))
			response := httptest.NewRecorder()

			handler.CreateFaction(response, request)

			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, tt.wantStatus)
			}
		})
	}
}

type fakeFactionService struct {
	createdFaction *entity.FactionEntity
	createErr      error
	factionName    string
}

func (s *fakeFactionService) GetFactionName(context.Context, string) (string, error) {
	return s.factionName, nil
}

func (s *fakeFactionService) CreateFaction(_ context.Context, faction *entity.FactionEntity) error {
	s.createdFaction = faction
	return s.createErr
}
