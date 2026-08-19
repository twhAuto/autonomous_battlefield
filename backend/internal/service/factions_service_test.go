package service

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/repository"
	"github.com/twh09/autonomous_battlefield/backend/internal/service/entity"
)

func TestCreateFaction(t *testing.T) {
	repo := &fakeFactionRepository{}
	service := NewFactionService(repo)

	err := service.CreateFaction(context.Background(), &entity.FactionEntity{
		ID:   "7f22e693-bacd-46bb-b942-64fa613cd388",
		Name: "  Northwind  ",
	})
	if err != nil {
		t.Fatalf("CreateFaction() returned an error: %v", err)
	}

	want := &entity.FactionEntity{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", Name: "Northwind"}
	if !reflect.DeepEqual(repo.createdFaction, want) {
		t.Fatalf("created faction = %#v, want %#v", repo.createdFaction, want)
	}
}

func TestCreateFactionRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name    string
		faction *entity.FactionEntity
	}{
		{name: "nil faction"},
		{name: "invalid ID", faction: &entity.FactionEntity{ID: "not-a-uuid", Name: "Northwind"}},
		{name: "empty name", faction: &entity.FactionEntity{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", Name: "  "}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeFactionRepository{}
			service := NewFactionService(repo)

			err := service.CreateFaction(context.Background(), tt.faction)
			if !errors.Is(err, ErrInvalidFaction) {
				t.Fatalf("CreateFaction() error = %v, want ErrInvalidFaction", err)
			}
			if repo.createdFaction != nil {
				t.Fatal("repository was called for invalid input")
			}
		})
	}
}

func TestCreateFactionTranslatesAlreadyExists(t *testing.T) {
	repo := &fakeFactionRepository{createErr: repository.ErrFactionAlreadyExists}
	service := NewFactionService(repo)

	err := service.CreateFaction(context.Background(), &entity.FactionEntity{
		ID:   "7f22e693-bacd-46bb-b942-64fa613cd388",
		Name: "Northwind",
	})
	if !errors.Is(err, ErrFactionAlreadyExists) {
		t.Fatalf("CreateFaction() error = %v, want ErrFactionAlreadyExists", err)
	}
}

type fakeFactionRepository struct {
	createdFaction *entity.FactionEntity
	createErr      error
}

func (r *fakeFactionRepository) GetFaction(context.Context, string) (*entity.FactionEntity, error) {
	return nil, repository.ErrFactionNotFound
}

func (r *fakeFactionRepository) CreateFaction(_ context.Context, faction *entity.FactionEntity) error {
	r.createdFaction = faction
	return r.createErr
}
