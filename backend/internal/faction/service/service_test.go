// Checks faction normalization, creation/editing limits, Unicode backgrounds, and
// repository/catalog delegation.

package service

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	"github.com/twh09/autonomous_battlefield/backend/internal/faction"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
)

func TestCreateFaction(t *testing.T) {
	repo := &fakeFactionRepository{}
	service := NewService(repo, fakeRulesets{})

	err := service.CreateFaction(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), ProfileInput{
		ID:   "7f22e693-bacd-46bb-b942-64fa613cd388",
		Name: "  Northwind  ", Doctrine: "  Hold the line.  ",
	})
	if err != nil {
		t.Fatalf("CreateFaction() returned an error: %v", err)
	}

	want := &faction.Faction{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", Name: "Northwind", Doctrine: "Hold the line.", OwnerID: "test-admin", Emblem: "star"}
	if !reflect.DeepEqual(repo.createdFaction, want) {
		t.Fatalf("created faction = %#v, want %#v", repo.createdFaction, want)
	}
}

func TestFactionWritesRejectInvalidInput(t *testing.T) {
	tests := []struct {
		name  string
		input ProfileInput
	}{
		{name: "empty input"},
		{name: "invalid ID", input: ProfileInput{ID: "not-a-uuid", Name: "Northwind", Doctrine: "Hold the line."}},
		{name: "empty name", input: ProfileInput{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", Name: "  "}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeFactionRepository{}
			service := NewService(repo, fakeRulesets{})

			for operation, write := range map[string]func(context.Context, ProfileInput) error{
				"create": service.CreateFaction,
				"update": service.UpdateFaction,
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
	service := NewService(repo, fakeRulesets{})

	err := service.CreateFaction(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), ProfileInput{
		ID:   "7f22e693-bacd-46bb-b942-64fa613cd388",
		Name: "Northwind", Doctrine: "Hold the line.",
	})
	if !errors.Is(err, ErrFactionAlreadyExists) {
		t.Fatalf("CreateFaction() error = %v, want ErrFactionAlreadyExists", err)
	}
}

var errLookup = errors.New("database unavailable")

func TestCreateFactionReplaysIdenticalRetry(t *testing.T) {
	const id = "7f22e693-bacd-46bb-b942-64fa613cd388"
	original := faction.Faction{ID: id, Name: "Northwind", Doctrine: "Hold the line.", Background: "Frost clans.", OwnerID: "self", Emblem: "star"}
	retry := ProfileInput{ID: id, Name: " Northwind ", Doctrine: "Hold the line.", Background: "Frost clans."}
	tests := []struct {
		name    string
		stored  faction.Faction
		caller  string
		input   ProfileInput
		getErr  error
		wantErr error
	}{
		{name: "identical retry replays", stored: original, caller: "self", input: retry},
		{name: "other caller conflicts", stored: original, caller: "other", input: retry, wantErr: ErrFactionAlreadyExists},
		{name: "changed name conflicts", stored: original, caller: "self", input: ProfileInput{ID: id, Name: "Southreach", Doctrine: retry.Doctrine, Background: retry.Background}, wantErr: ErrFactionAlreadyExists},
		{name: "changed doctrine conflicts", stored: original, caller: "self", input: ProfileInput{ID: id, Name: retry.Name, Doctrine: "Attack.", Background: retry.Background}, wantErr: ErrFactionAlreadyExists},
		{name: "changed background conflicts", stored: original, caller: "self", input: ProfileInput{ID: id, Name: retry.Name, Doctrine: retry.Doctrine}, wantErr: ErrFactionAlreadyExists},
		{name: "lookup failure surfaces", stored: original, caller: "self", input: retry, getErr: errLookup, wantErr: errLookup},
		{name: "deleted before lookup conflicts", stored: original, caller: "self", input: retry, getErr: ErrFactionNotFound, wantErr: ErrFactionAlreadyExists},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stored := tt.stored
			repo := &fakeFactionRepository{createErr: ErrFactionAlreadyExists, storedFaction: &stored, getErr: tt.getErr}
			ctx := authn.WithActor(context.Background(), authn.Actor{ID: tt.caller, Role: authn.Player})
			if err := NewService(repo, fakeRulesets{}).CreateFaction(ctx, tt.input); !errors.Is(err, tt.wantErr) {
				t.Fatalf("CreateFaction() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestDoctrineValidationAndUpdate(t *testing.T) {
	repo := &fakeFactionRepository{}
	service := NewService(repo, fakeRulesets{})
	for _, doctrine := range []string{"", "  ", strings.Repeat("界", 4001)} {
		input := ProfileInput{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", Name: "A", Doctrine: doctrine}
		if err := service.CreateFaction(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), input); !errors.Is(err, ErrInvalidFaction) {
			t.Fatalf("create: %v", err)
		}
		if err := service.UpdateFaction(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), input); !errors.Is(err, ErrInvalidFaction) {
			t.Fatalf("update: %v", err)
		}
	}
	input := ProfileInput{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", Name: " A ", Doctrine: " " + strings.Repeat("界", 4000) + " "}
	if err := service.CreateFaction(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), input); err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateFaction(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), input); err != nil {
		t.Fatal(err)
	}
	if repo.createdFaction.Name != "A" || repo.createdFaction.Doctrine != strings.Repeat("界", 4000) {
		t.Fatal("profile not trimmed")
	}
	repo.createErr = ErrFactionNotFound
	if err := service.UpdateFaction(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), input); !errors.Is(err, ErrFactionNotFound) {
		t.Fatal(err)
	}
}

func TestListFactions(t *testing.T) {
	want := []faction.Faction{
		{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", Name: "Northwind", Doctrine: "Hold the line."},
		{ID: "4db8d152-1e04-4d10-8824-859c6ed72878", Name: "Verdant Accord"},
	}
	repo := &fakeFactionRepository{factions: want}
	service := NewService(repo, fakeRulesets{})

	got, err := service.ListFactions(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}))
	if err != nil {
		t.Fatalf("ListFactions() returned an error: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListFactions() = %#v, want %#v", got, want)
	}
}

func TestListRulesets(t *testing.T) {
	if got := NewService(&fakeFactionRepository{}, fakeRulesets{}).ListRulesets(); got == nil || len(got) != 0 {
		t.Fatalf("rulesets from an empty catalog = %#v, want an empty slice", got)
	}
	ruleset := game.Ruleset{ID: "castle-siege-v1"}
	if got := NewService(&fakeFactionRepository{}, fakeRulesets{rulesets: []game.Ruleset{ruleset}}).ListRulesets(); len(got) != 1 || got[0].ID != ruleset.ID {
		t.Fatalf("rulesets = %#v", got)
	}
}

func TestCreateFactionRecordsTheCallerAsOwner(t *testing.T) {
	repo := &fakeFactionRepository{}
	ctx := authn.WithActor(context.Background(), authn.Actor{ID: "player-7", Role: authn.Player})
	if err := NewService(repo, fakeRulesets{}).CreateFaction(ctx, ProfileInput{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", Name: "A", Doctrine: "Scout"}); err != nil {
		t.Fatal(err)
	}
	if repo.createdFaction.OwnerID != "player-7" {
		t.Fatalf("owner = %q, want player-7", repo.createdFaction.OwnerID)
	}
}

func TestCreateFactionLimitsOnlyPlayers(t *testing.T) {
	input := ProfileInput{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", Name: "A", Doctrine: "Scout"}
	for name, test := range map[string]struct {
		ctx  context.Context
		want int
	}{
		"player":   {authn.WithActor(context.Background(), authn.Actor{ID: "player-7", Role: authn.Player}), playerFactionLimit},
		"tester":   {authn.WithActor(context.Background(), authn.Actor{ID: "tester-7", Role: authn.Tester}), 0},
		"admin":    {authn.WithActor(context.Background(), authn.Actor{ID: "admin-7", Role: authn.Admin}), 0},
		"no actor": {context.Background(), 0},
	} {
		t.Run(name, func(t *testing.T) {
			repo := &fakeFactionRepository{ownerLimit: -1}
			if err := NewService(repo, fakeRulesets{}).CreateFaction(test.ctx, input); err != nil || repo.ownerLimit != test.want {
				t.Fatalf("owner limit = %d, error = %v, want limit %d", repo.ownerLimit, err, test.want)
			}
		})
	}
	repo := &fakeFactionRepository{createErr: ErrFactionLimitReached}
	player := authn.WithActor(context.Background(), authn.Actor{ID: "player-7", Role: authn.Player})
	if err := NewService(repo, fakeRulesets{}).CreateFaction(player, input); !errors.Is(err, ErrFactionLimitReached) {
		t.Fatalf("error = %v, want ErrFactionLimitReached", err)
	}
}

func TestDeleteFaction(t *testing.T) {
	id := "7f22e693-bacd-46bb-b942-64fa613cd388"
	repo := &fakeFactionRepository{}
	service := NewService(repo, fakeRulesets{})
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
	ownerLimit     int
	storedFaction  *faction.Faction
	createErr      error
	getErr         error
	factions       []faction.Faction
	deletedID      string
	deleteErr      error
}

type fakeRulesets struct{ rulesets []game.Ruleset }

func (f fakeRulesets) Rulesets() []game.Ruleset { return f.rulesets }

func (r *fakeFactionRepository) GetFaction(context.Context, string) (*faction.Faction, error) {
	return r.storedFaction, r.getErr
}

func (r *fakeFactionRepository) ListFactions(context.Context) ([]faction.Faction, error) {
	return r.factions, nil
}

func (r *fakeFactionRepository) CreateFaction(_ context.Context, stored faction.Faction, ownerLimit int) error {
	r.createdFaction = &stored
	r.ownerLimit = ownerLimit
	return r.createErr
}

func (r *fakeFactionRepository) DeleteFaction(_ context.Context, id string) error {
	r.deletedID = id
	return r.deleteErr
}

func (r *fakeFactionRepository) UpdateFaction(ctx context.Context, stored faction.Faction) error {
	return r.CreateFaction(ctx, stored, 0)
}

func TestBackgroundCreationAndEditing(t *testing.T) {
	repo := &fakeFactionRepository{}
	s := NewService(repo, fakeRulesets{})
	for _, background := range []string{"", " Old roots. ", " " + strings.Repeat("😀", 1000) + " "} {
		input := ProfileInput{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", Name: "Name", Doctrine: "Doctrine", Background: background}
		if err := s.CreateFaction(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), input); err != nil {
			t.Fatal(err)
		}
		if repo.createdFaction.Background != strings.TrimSpace(background) {
			t.Fatal("background not preserved")
		}
		if err := s.UpdateFaction(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), input); err != nil {
			t.Fatal(err)
		}
		if repo.createdFaction.Background != strings.TrimSpace(background) {
			t.Fatal("background edit not preserved")
		}
	}
	input := ProfileInput{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", Name: "Name", Doctrine: "Doctrine", Background: strings.Repeat("😀", 1001)}
	if err := s.CreateFaction(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), input); !errors.Is(err, ErrInvalidFaction) {
		t.Fatal(err)
	}
	if err := s.UpdateFaction(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), input); !errors.Is(err, ErrInvalidFaction) {
		t.Fatal(err)
	}
}

func TestFactionOwnershipAllRoles(t *testing.T) {
	for _, role := range []authn.Role{authn.Player, authn.Tester, authn.Admin} {
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
	s := NewService(repo, fakeRulesets{})
	ctx := authn.WithActor(context.Background(), authn.Actor{ID: "self", Role: role})
	allowed := role == authn.Admin || owner == "self"
	e := s.UpdateFaction(ctx, ProfileInput{ID: id, Name: "New", Doctrine: "Defend"})
	if (e == nil) != allowed || (!allowed && repo.createdFaction != nil) {
		t.Fatalf("edit %s %s: %v", role, owner, e)
	}
	e = s.DeleteFaction(ctx, id)
	if (e == nil) != allowed || (!allowed && repo.deletedID != "") {
		t.Fatalf("delete %s %s: %v", role, owner, e)
	}
}

func TestEmblemCreationRetryAndUpdate(t *testing.T) {
	for _, emblem := range []string{"star", "crown", "shield", "swords", "wolf", "raven", "serpent", "tree", "leaf", "flame", "sun", "moon"} {
		t.Run(emblem, func(t *testing.T) { checkEmblemCreationRetryAndUpdate(t, emblem) })
	}
}

func checkEmblemCreationRetryAndUpdate(t *testing.T, emblem string) {
	t.Helper()
	ctx := authn.WithActor(context.Background(), authn.Actor{ID: "self", Role: authn.Admin})
	input := ProfileInput{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", Name: "Northwind", Doctrine: "Defend", Emblem: emblem}
	repo := &fakeFactionRepository{}
	s := NewService(repo, fakeRulesets{})
	if err := s.CreateFaction(ctx, input); err != nil || repo.createdFaction.Emblem != emblem {
		t.Fatalf("create %s: %v", emblem, err)
	}
	repo.storedFaction = repo.createdFaction
	repo.createErr = ErrFactionAlreadyExists
	if err := s.CreateFaction(ctx, input); err != nil {
		t.Fatalf("retry %s: %v", emblem, err)
	}
	input.Emblem = "wolf"
	if emblem == "wolf" {
		input.Emblem = "moon"
	}
	if err := s.CreateFaction(ctx, input); !errors.Is(err, ErrFactionAlreadyExists) {
		t.Fatalf("changed emblem retry: %v", err)
	}
	repo.createErr = nil
	if err := s.UpdateFaction(ctx, input); err != nil || repo.createdFaction.Emblem != input.Emblem {
		t.Fatalf("edit: %v", err)
	}
}

func TestFactionWritesRejectUnknownEmblems(t *testing.T) {
	ctx := authn.WithActor(context.Background(), authn.Actor{ID: "self", Role: authn.Admin})
	input := ProfileInput{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", Name: "Northwind", Doctrine: "Defend"}
	s := NewService(&fakeFactionRepository{}, fakeRulesets{})
	for _, emblem := range []string{"dragon", "WOLF", " wolf ", "<svg onload=alert(1)>"} {
		input.Emblem = emblem
		if err := s.CreateFaction(ctx, input); !errors.Is(err, ErrInvalidFaction) {
			t.Fatalf("invalid create %q: %v", emblem, err)
		}
		if err := s.UpdateFaction(ctx, input); !errors.Is(err, ErrInvalidFaction) {
			t.Fatalf("invalid edit %q: %v", emblem, err)
		}
	}
}

func TestLegacyFactionUpdatePreservesEmblem(t *testing.T) {
	ctx := authn.WithActor(context.Background(), authn.Actor{ID: "self", Role: authn.Admin})
	input := ProfileInput{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", Name: "Northwind", Doctrine: "Defend"}
	repo := &fakeFactionRepository{}
	s := NewService(repo, fakeRulesets{})
	if err := s.UpdateFaction(ctx, input); err != nil || repo.createdFaction.Emblem != "" {
		t.Fatalf("legacy edit must preserve stored emblem: %v", err)
	}
}
