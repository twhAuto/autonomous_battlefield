package service

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/faction"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
)

func TestCreateFaction(t *testing.T) {
	repo := &fakeFactionRepository{}
	service := NewService(repo)

	err := service.CreateFaction(context.Background(), CreateInput{
		ID:   "7f22e693-bacd-46bb-b942-64fa613cd388",
		Name: "  Northwind  ", Doctrine: "  Hold the line.  ",
	})
	if err != nil {
		t.Fatalf("CreateFaction() returned an error: %v", err)
	}

	want := &faction.Faction{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", Name: "Northwind", Doctrine: "Hold the line."}
	if !reflect.DeepEqual(repo.createdFaction, want) {
		t.Fatalf("created faction = %#v, want %#v", repo.createdFaction, want)
	}
}

func TestFactionWritesRejectInvalidInput(t *testing.T) {
	tests := []struct {
		name  string
		input CreateInput
	}{
		{name: "empty input"},
		{name: "invalid ID", input: CreateInput{ID: "not-a-uuid", Name: "Northwind", Doctrine: "Hold the line."}},
		{name: "empty name", input: CreateInput{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", Name: "  "}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeFactionRepository{}
			service := NewService(repo)

			for operation, write := range map[string]func(context.Context, CreateInput) error{
				"create": service.CreateFaction,
				"update": service.(Updater).UpdateFaction,
			} {
				if err := write(context.Background(), tt.input); !errors.Is(err, ErrInvalidFaction) {
					t.Fatalf("%s error = %v, want ErrInvalidFaction", operation, err)
				}
				if repo.createdFaction != nil {
					t.Fatalf("%s called repository for invalid input", operation)
				}
			}
		})
	}
}

func TestCreateFactionReturnsAlreadyExists(t *testing.T) {
	repo := &fakeFactionRepository{createErr: ErrFactionAlreadyExists}
	service := NewService(repo)

	err := service.CreateFaction(context.Background(), CreateInput{
		ID:   "7f22e693-bacd-46bb-b942-64fa613cd388",
		Name: "Northwind", Doctrine: "Hold the line.",
	})
	if !errors.Is(err, ErrFactionAlreadyExists) {
		t.Fatalf("CreateFaction() error = %v, want ErrFactionAlreadyExists", err)
	}
}

func TestDoctrineValidationAndUpdate(t *testing.T) {
	repo := &fakeFactionRepository{}
	service := NewService(repo)
	for _, doctrine := range []string{"", "  ", strings.Repeat("界", 4001)} {
		input := CreateInput{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", Name: "A", Doctrine: doctrine}
		if err := service.CreateFaction(context.Background(), input); !errors.Is(err, ErrInvalidFaction) {
			t.Fatalf("create: %v", err)
		}
		if err := service.(Updater).UpdateFaction(context.Background(), input); !errors.Is(err, ErrInvalidFaction) {
			t.Fatalf("update: %v", err)
		}
	}
	input := CreateInput{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", Name: " A ", Doctrine: " " + strings.Repeat("界", 4000) + " "}
	if err := service.CreateFaction(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if err := service.(Updater).UpdateFaction(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if repo.createdFaction.Name != "A" || repo.createdFaction.Doctrine != strings.Repeat("界", 4000) {
		t.Fatal("profile not trimmed")
	}
	repo.createErr = ErrFactionNotFound
	if err := service.(Updater).UpdateFaction(context.Background(), input); !errors.Is(err, ErrFactionNotFound) {
		t.Fatal(err)
	}
}

func TestListFactions(t *testing.T) {
	want := []faction.Faction{
		{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", Name: "Northwind", Doctrine: "Hold the line."},
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

func TestListRulesets(t *testing.T) {
	withoutCatalog := NewService(&fakeFactionRepository{})
	if got := withoutCatalog.(RulesetLister).ListRulesets(); len(got) != 0 {
		t.Fatalf("rulesets without catalog = %#v", got)
	}
	ruleset := game.Ruleset{ID: "castle-siege-v1"}
	withCatalog := NewService(&fakeFactionRepository{}, fakeRulesets{ruleset: ruleset})
	if got := withCatalog.(RulesetLister).ListRulesets(); len(got) != 1 || got[0].ID != ruleset.ID {
		t.Fatalf("rulesets = %#v", got)
	}
}

func TestGetFactionName(t *testing.T) {
	repo := &fakeFactionRepository{storedFaction: &faction.Faction{Name: "Northwind", Doctrine: "Hold the line."}}
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

func TestDeleteFaction(t *testing.T) {
	id := "7f22e693-bacd-46bb-b942-64fa613cd388"
	repo := &fakeFactionRepository{}
	service := NewService(repo)
	if err := service.DeleteFaction(context.Background(), id); err != nil || repo.deletedID != id {
		t.Fatalf("deleted=%q err=%v", repo.deletedID, err)
	}
	if err := service.DeleteFaction(context.Background(), "not-a-uuid"); !errors.Is(err, ErrInvalidFaction) {
		t.Fatalf("invalid ID error=%v", err)
	}
	repo.deleteErr = ErrFactionNotFound
	if err := service.DeleteFaction(context.Background(), id); !errors.Is(err, ErrFactionNotFound) {
		t.Fatalf("not found error=%v", err)
	}
}

type fakeFactionRepository struct {
	createdFaction *faction.Faction
	storedFaction  *faction.Faction
	createErr      error
	getErr         error
	factions       []faction.Faction
	deletedID      string
	deleteErr      error
}

type fakeRulesets struct{ ruleset game.Ruleset }

func (f fakeRulesets) Rulesets() []game.Ruleset { return []game.Ruleset{f.ruleset} }

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

func (r *fakeFactionRepository) DeleteFaction(_ context.Context, id string) error {
	r.deletedID = id
	return r.deleteErr
}

func (r *fakeFactionRepository) UpdateFaction(_ context.Context, stored faction.Faction) error {
	r.createdFaction = &stored
	return r.createErr
}

func TestBackgroundCreationAndEditing(t *testing.T) {
	repo := &fakeFactionRepository{}
	s := NewService(repo)
	for _, background := range []string{"", " Old roots. ", " " + strings.Repeat("😀", 1000) + " "} {
		input := CreateInput{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", Name: "Name", Doctrine: "Doctrine", Background: background}
		if err := s.CreateFaction(context.Background(), input); err != nil {
			t.Fatal(err)
		}
		if repo.createdFaction.Background != strings.TrimSpace(background) {
			t.Fatal("background not preserved")
		}
		if err := s.(Updater).UpdateFaction(context.Background(), input); err != nil {
			t.Fatal(err)
		}
		if repo.createdFaction.Background != strings.TrimSpace(background) {
			t.Fatal("background edit not preserved")
		}
	}
	input := CreateInput{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", Name: "Name", Doctrine: "Doctrine", Background: strings.Repeat("😀", 1001)}
	if err := s.CreateFaction(context.Background(), input); !errors.Is(err, ErrInvalidFaction) {
		t.Fatal(err)
	}
	if err := s.(Updater).UpdateFaction(context.Background(), input); !errors.Is(err, ErrInvalidFaction) {
		t.Fatal(err)
	}
}
