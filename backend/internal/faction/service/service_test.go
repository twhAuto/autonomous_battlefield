package service

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/faction"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
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

func TestCreateFactionValidatesAndPricesBlueprint(t *testing.T) {
	ruleset := game.Ruleset{ID: "castle-siege-v1", Version: "1", DesignBudget: 120, StartingGold: 60, Units: []game.UnitType{{ID: "vanguard", Cost: 35, Health: 70, Attack: 9, Defense: 7, Speed: 4, Movement: 1, Range: 1, Vision: 2}}, Upgrades: []game.Upgrade{{ID: "arsenal", Cost: 20, AttackBonus: 2}}, Buildings: game.BuildingRules{CastleHealth: 300, CastleDefense: 8, CastleVision: 4, CastleIncome: 10, EconomyCost: 50, EconomyHealth: 60, EconomyDefense: 2, EconomyVision: 2, EconomyIncome: 10, MaximumEconomyCount: 2}, Combat: game.CombatRules{MinimumDamage: 1, DefendAttackPercent: 80, DefendDamageTakenPercent: 70, LootPercent: 50}}
	catalog := fakeRulesets{ruleset: ruleset}
	repo := &fakeFactionRepository{}
	service := NewService(repo, catalog)
	blueprint := game.Blueprint{Units: []game.UnitSelection{{Type: "vanguard", Count: 1}}, UpgradeIDs: []string{"arsenal"}, RecruitmentPriority: []string{"vanguard"}}
	err := service.CreateFaction(context.Background(), CreateInput{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", Name: "Northwind", RulesetID: ruleset.ID, Blueprint: blueprint})
	if err != nil || repo.createdFaction.RulesetID != ruleset.ID {
		t.Fatalf("created = %#v, err = %v", repo.createdFaction, err)
	}
	invalid := blueprint
	invalid.Units[0].Count = 4
	if err := service.CreateFaction(context.Background(), CreateInput{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", Name: "Northwind", RulesetID: ruleset.ID, Blueprint: invalid}); !errors.Is(err, ErrInvalidBlueprint) {
		t.Fatalf("error = %v", err)
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

func (f fakeRulesets) Ruleset(id string) (game.Ruleset, bool) { return f.ruleset, id == f.ruleset.ID }
func (f fakeRulesets) Rulesets() []game.Ruleset               { return []game.Ruleset{f.ruleset} }

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
