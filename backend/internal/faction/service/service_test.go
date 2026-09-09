// Checks faction normalization, creation/editing limits, Unicode backgrounds, and
// repository/catalog delegation.

package service

import (
	"context"
	"errors"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	"reflect"
	"strings"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/faction"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
)

func TestCreateFaction(t *testing.T) {
	repo := &fakeFactionRepository{}
	service := NewService(repo)

	err := service.CreateFaction(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), CreateInput{
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
				if err := write(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), tt.input); !errors.Is(err, ErrInvalidFaction) {
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

	err := service.CreateFaction(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), CreateInput{
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
		if err := service.CreateFaction(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), input); !errors.Is(err, ErrInvalidFaction) {
			t.Fatalf("create: %v", err)
		}
		if err := service.(Updater).UpdateFaction(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), input); !errors.Is(err, ErrInvalidFaction) {
			t.Fatalf("update: %v", err)
		}
	}
	input := CreateInput{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", Name: " A ", Doctrine: " " + strings.Repeat("界", 4000) + " "}
	if err := service.CreateFaction(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), input); err != nil {
		t.Fatal(err)
	}
	if err := service.(Updater).UpdateFaction(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), input); err != nil {
		t.Fatal(err)
	}
	if repo.createdFaction.Name != "A" || repo.createdFaction.Doctrine != strings.Repeat("界", 4000) {
		t.Fatal("profile not trimmed")
	}
	repo.createErr = ErrFactionNotFound
	if err := service.(Updater).UpdateFaction(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), input); !errors.Is(err, ErrFactionNotFound) {
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

	got, err := service.ListFactions(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}))
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
	name, err := service.GetFactionName(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), "id")
	if err != nil || name != "Northwind" {
		t.Fatalf("GetFactionName() = %q, %v", name, err)
	}

	repo.getErr = ErrFactionNotFound
	if _, err := service.GetFactionName(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), "missing"); !errors.Is(err, ErrFactionNotFound) {
		t.Fatalf("GetFactionName() error = %v, want ErrFactionNotFound", err)
	}
}

func TestDeleteFaction(t *testing.T) {
	id := "7f22e693-bacd-46bb-b942-64fa613cd388"
	repo := &fakeFactionRepository{}
	service := NewService(repo)
	if err := service.DeleteFaction(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), id); err != nil || repo.deletedID != id {
		t.Fatalf("deleted=%q err=%v", repo.deletedID, err)
	}
	if err := service.DeleteFaction(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), "not-a-uuid"); !errors.Is(err, ErrInvalidFaction) {
		t.Fatalf("invalid ID error=%v", err)
	}
	repo.deleteErr = ErrFactionNotFound
	if err := service.DeleteFaction(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), id); !errors.Is(err, ErrFactionNotFound) {
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

func (r *fakeFactionRepository) UpdateFaction(ctx context.Context, stored faction.Faction) error {
	return r.CreateFaction(ctx, stored)
}

func TestBackgroundCreationAndEditing(t *testing.T) {
	repo := &fakeFactionRepository{}
	s := NewService(repo)
	for _, background := range []string{"", " Old roots. ", " " + strings.Repeat("😀", 1000) + " "} {
		input := CreateInput{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", Name: "Name", Doctrine: "Doctrine", Background: background}
		if err := s.CreateFaction(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), input); err != nil {
			t.Fatal(err)
		}
		if repo.createdFaction.Background != strings.TrimSpace(background) {
			t.Fatal("background not preserved")
		}
		if err := s.(Updater).UpdateFaction(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), input); err != nil {
			t.Fatal(err)
		}
		if repo.createdFaction.Background != strings.TrimSpace(background) {
			t.Fatal("background edit not preserved")
		}
	}
	input := CreateInput{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", Name: "Name", Doctrine: "Doctrine", Background: strings.Repeat("😀", 1001)}
	if err := s.CreateFaction(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), input); !errors.Is(err, ErrInvalidFaction) {
		t.Fatal(err)
	}
	if err := s.(Updater).UpdateFaction(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), input); !errors.Is(err, ErrInvalidFaction) {
		t.Fatal(err)
	}
}

func TestFactionOwnershipAllRoles(t *testing.T) {
	for _, role := range []authn.Role{authn.Player, authn.Developer, authn.Admin} {
		for _, owner := range []string{"self", "other", authn.LegacyOwner} {
			t.Run(string(role)+owner, func(t *testing.T) {
				checkFactionOwnership(t, role, owner)
			})
		}
	}
}

func checkFactionOwnership(t *testing.T, role authn.Role, owner string) {
	t.Helper()
	const id = "7f22e693-bacd-46bb-b942-64fa613cd388"
	repo := &fakeFactionRepository{storedFaction: &faction.Faction{ID: id, OwnerID: owner}}
	s := NewService(repo)
	ctx := authn.WithActor(context.Background(), authn.Actor{ID: "self", Role: role})
	allowed := role == authn.Admin || owner == "self"
	e := s.(Updater).UpdateFaction(ctx, CreateInput{ID: id, Name: "New", Doctrine: "Defend"})
	if (e == nil) != allowed || (!allowed && repo.createdFaction != nil) {
		t.Fatalf("edit %s %s: %v", role, owner, e)
	}
	e = s.DeleteFaction(ctx, id)
	if (e == nil) != allowed || (!allowed && repo.deletedID != "") {
		t.Fatalf("delete %s %s: %v", role, owner, e)
	}
}
