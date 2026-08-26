package service

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/faction"
)

func TestCreateFaction(t *testing.T) {
	repo := &fakeFactionRepository{}
	service := NewService(repo)

	err := service.CreateFaction(context.Background(), CreateInput{
		ID:   "7f22e693-bacd-46bb-b942-64fa613cd388",
		Name: "  Northwind  ",
	})
	if err != nil {
		t.Fatalf("CreateFaction() returned an error: %v", err)
	}

	want := &faction.Faction{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", Name: "Northwind"}
	if !reflect.DeepEqual(repo.createdFaction, want) {
		t.Fatalf("created faction = %#v, want %#v", repo.createdFaction, want)
	}
}

func TestCreateFactionRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name  string
		input CreateInput
	}{
		{name: "empty input"},
		{name: "invalid ID", input: CreateInput{ID: "not-a-uuid", Name: "Northwind"}},
		{name: "empty name", input: CreateInput{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", Name: "  "}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeFactionRepository{}
			service := NewService(repo)

			err := service.CreateFaction(context.Background(), tt.input)
			if !errors.Is(err, ErrInvalidFaction) {
				t.Fatalf("CreateFaction() error = %v, want ErrInvalidFaction", err)
			}
			if repo.createdFaction != nil {
				t.Fatal("repository was called for invalid input")
			}
		})
	}
}

func TestCreateFactionReturnsAlreadyExists(t *testing.T) {
	repo := &fakeFactionRepository{createErr: ErrFactionAlreadyExists}
	service := NewService(repo)

	err := service.CreateFaction(context.Background(), CreateInput{
		ID:   "7f22e693-bacd-46bb-b942-64fa613cd388",
		Name: "Northwind",
	})
	if !errors.Is(err, ErrFactionAlreadyExists) {
		t.Fatalf("CreateFaction() error = %v, want ErrFactionAlreadyExists", err)
	}
}

func TestListFactions(t *testing.T) {
	want := []faction.Faction{
		{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", Name: "Northwind"},
		{ID: "4db8d152-1e04-4d10-8824-859c6ed72878", Name: "Verdant Accord"},
	}
	repo := &fakeFactionRepository{factions: want}
	service := NewService(repo)

	got, err := service.ListFactions(context.Background())
	if err != nil {
		t.Fatalf("ListFactions() returned an error: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListFactions() = %#v, want %#v", got, want)
	}
}

func TestGetFactionName(t *testing.T) {
	repo := &fakeFactionRepository{storedFaction: &faction.Faction{Name: "Northwind"}}
	service := NewService(repo)
	name, err := service.GetFactionName(context.Background(), "id")
	if err != nil || name != "Northwind" {
		t.Fatalf("GetFactionName() = %q, %v", name, err)
	}

	repo.getErr = ErrFactionNotFound
	if _, err := service.GetFactionName(context.Background(), "missing"); !errors.Is(err, ErrFactionNotFound) {
		t.Fatalf("GetFactionName() error = %v, want ErrFactionNotFound", err)
	}
}

type fakeFactionRepository struct {
	createdFaction *faction.Faction
	storedFaction  *faction.Faction
	createErr      error
	getErr         error
	factions       []faction.Faction
}

func (r *fakeFactionRepository) GetFaction(context.Context, string) (*faction.Faction, error) {
	return r.storedFaction, r.getErr
}

func (r *fakeFactionRepository) ListFactions(context.Context) ([]faction.Faction, error) {
	return r.factions, nil
}

func (r *fakeFactionRepository) CreateFaction(_ context.Context, stored faction.Faction) error {
	r.createdFaction = &stored
	return r.createErr
}
