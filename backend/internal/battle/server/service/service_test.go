package service

import (
	"context"
	"errors"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/battle/simulation/controller"
	"github.com/twh09/autonomous_battlefield/backend/internal/faction"
	factionservice "github.com/twh09/autonomous_battlefield/backend/internal/faction/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
)

func TestCreateBattleResolvesFactionsAndPersistsSnapshot(t *testing.T) {
	battleRepo := &fakeBattleRepo{}
	factions := &battleFactionRepo{factions: map[string]*faction.Faction{
		"4db8d152-1e04-4d10-8824-859c6ed72878": {ID: "4db8d152-1e04-4d10-8824-859c6ed72878", Name: "Northwind"},
		"41ac3a2f-3371-4de1-b0f6-70600fcd9052": {ID: "41ac3a2f-3371-4de1-b0f6-70600fcd9052", Name: "Verdant"},
	}}
	catalog := &fakeScenarioCatalog{resolved: game.Scenario{ID: "castle-siege-v1"}, digest: "digest"}
	service := NewService(battleRepo, factions, catalog, 10_000)

	run, err := service.CreateBattle(context.Background(), CreateBattleInput{
		ID: "7f22e693-bacd-46bb-b942-64fa613cd388", ScenarioID: "skirmish-v1",
		FactionIDs: []string{"4db8d152-1e04-4d10-8824-859c6ed72878", "41ac3a2f-3371-4de1-b0f6-70600fcd9052"}, TickCount: 100,
	})
	if err != nil {
		t.Fatalf("CreateBattle() returned an error: %v", err)
	}
	if run.Status != "queued" || battleRepo.created.ScenarioDigest != "digest" || battleRepo.created.RequestedTicks != 100 {
		t.Fatalf("run/created = %#v / %#v", run, battleRepo.created)
	}
	if catalog.identities[0].Name != "Northwind" || catalog.identities[1].Name != "Verdant" {
		t.Fatalf("resolved identities = %#v", catalog.identities)
	}
}

func TestCreateBattleValidationAndDependencies(t *testing.T) {
	valid := CreateBattleInput{
		ID: "7f22e693-bacd-46bb-b942-64fa613cd388", ScenarioID: "skirmish-v1",
		FactionIDs: []string{"4db8d152-1e04-4d10-8824-859c6ed72878", "41ac3a2f-3371-4de1-b0f6-70600fcd9052"}, TickCount: 100,
	}
	t.Run("invalid", func(t *testing.T) {
		tests := []struct {
			name   string
			mutate func(*CreateBattleInput)
		}{
			{name: "zero ticks", mutate: func(input *CreateBattleInput) { input.TickCount = 0 }},
			{name: "too many ticks", mutate: func(input *CreateBattleInput) { input.TickCount = 10_001 }},
			{name: "duplicate factions", mutate: func(input *CreateBattleInput) { input.FactionIDs[1] = input.FactionIDs[0] }},
			{name: "malformed ID", mutate: func(input *CreateBattleInput) { input.ID = "bad" }},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				service := NewService(&fakeBattleRepo{}, &battleFactionRepo{}, &fakeScenarioCatalog{}, 10_000)
				input := valid
				input.FactionIDs = append([]string(nil), valid.FactionIDs...)
				tt.mutate(&input)
				if _, err := service.CreateBattle(context.Background(), input); !errors.Is(err, ErrInvalidBattle) {
					t.Fatalf("error = %v, want ErrInvalidBattle", err)
				}
			})
		}
	})
	t.Run("scenario tick limit", func(t *testing.T) {
		factions := &battleFactionRepo{factions: map[string]*faction.Faction{
			valid.FactionIDs[0]: {ID: valid.FactionIDs[0], Name: "A"},
			valid.FactionIDs[1]: {ID: valid.FactionIDs[1], Name: "B"},
		}}
		catalog := &fakeScenarioCatalog{resolved: serviceTestScenario([2]FactionIdentity{{ID: valid.FactionIDs[0], Name: "A"}, {ID: valid.FactionIDs[1], Name: "B"}})}
		service := NewService(&fakeBattleRepo{}, factions, catalog, 10_000)
		input := valid
		input.TickCount = 501
		if _, err := service.CreateBattle(context.Background(), input); !errors.Is(err, ErrInvalidBattle) {
			t.Fatalf("error = %v, want ErrInvalidBattle", err)
		}
	})
	t.Run("missing faction", func(t *testing.T) {
		service := NewService(&fakeBattleRepo{}, &battleFactionRepo{}, &fakeScenarioCatalog{}, 10_000)
		if _, err := service.CreateBattle(context.Background(), valid); !errors.Is(err, ErrBattleFactionMissing) {
			t.Fatalf("error = %v, want ErrBattleFactionMissing", err)
		}
	})
	t.Run("missing scenario", func(t *testing.T) {
		factions := &battleFactionRepo{factions: map[string]*faction.Faction{
			valid.FactionIDs[0]: {ID: valid.FactionIDs[0], Name: "A"}, valid.FactionIDs[1]: {ID: valid.FactionIDs[1], Name: "B"},
		}}
		service := NewService(&fakeBattleRepo{}, factions, &fakeScenarioCatalog{err: ErrScenarioNotFound}, 10_000)
		if _, err := service.CreateBattle(context.Background(), valid); !errors.Is(err, ErrScenarioNotFound) {
			t.Fatalf("error = %v, want ErrScenarioNotFound", err)
		}
	})
}

func TestListBattleEventsDefaultsToCurrentAttempt(t *testing.T) {
	repo := &fakeBattleRepo{
		run:    &BattleRun{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", Attempt: 2},
		events: []StoredBattleEvent{{Attempt: 2, Sequence: 5}},
	}
	service := NewService(repo, &battleFactionRepo{}, &fakeScenarioCatalog{}, 10_000)
	page, err := service.ListBattleEvents(context.Background(), repo.run.ID, nil, 0, 100, true)
	if err != nil {
		t.Fatalf("ListBattleEvents() returned an error: %v", err)
	}
	if page.Attempt != 2 || page.NextCursor == nil || *page.NextCursor != 5 || repo.listAttempt != 2 || !repo.listLatest {
		t.Fatalf("page = %#v, queried attempt = %d", page, repo.listAttempt)
	}
}

func TestGetBattleRejectsMalformedID(t *testing.T) {
	service := NewService(&fakeBattleRepo{}, &battleFactionRepo{}, &fakeScenarioCatalog{}, 10_000)
	if _, err := service.GetBattle(context.Background(), "not-a-uuid"); !errors.Is(err, ErrInvalidBattle) {
		t.Fatalf("GetBattle() error = %v, want ErrInvalidBattle", err)
	}
}

func TestBattleServiceDelegatesCatalogAndRepositoryReads(t *testing.T) {
	validID := "7f22e693-bacd-46bb-b942-64fa613cd388"
	catalog := &fakeScenarioCatalog{summaries: []ScenarioSummary{{ID: "skirmish-v1"}}}
	repository := &fakeBattleRepo{run: &BattleRun{ID: validID}}
	service := NewService(repository, &battleFactionRepo{}, catalog, 10_000)
	if summaries := service.ListScenarios(); len(summaries) != 1 || summaries[0].ID != "skirmish-v1" {
		t.Fatalf("ListScenarios() = %#v", summaries)
	}
	run, err := service.GetBattle(context.Background(), validID)
	if err != nil || run.ID != validID {
		t.Fatalf("GetBattle() = %#v, %v", run, err)
	}

	repository.getErr = errors.New("database unavailable")
	if _, err := service.GetBattle(context.Background(), validID); err == nil {
		t.Fatal("GetBattle() returned no repository error")
	}
}

func TestFactionViewFiltersPersistedState(t *testing.T) {
	validID := "7f22e693-bacd-46bb-b942-64fa613cd388"
	state := game.State{Factions: []game.FactionState{{FactionID: "a"}, {FactionID: "b"}}, Units: []game.Unit{{ID: "a-unit", FactionID: "a", Position: game.Position{X: 1, Y: 1}, Health: 10, Vision: 1}, {ID: "b-unit", FactionID: "b", Position: game.Position{X: 12, Y: 9}, Health: 10}}, Buildings: []game.Building{{ID: "b-castle", FactionID: "b", Type: game.BuildingCastle, Position: game.Position{X: 13, Y: 5}, Health: 300}}}
	repository := &fakeBattleRepo{run: &BattleRun{ID: validID, ScenarioSnapshot: game.Scenario{Map: serviceTestScenario([2]FactionIdentity{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}}).Map}, CurrentState: state}}
	service := NewService(repository, &battleFactionRepo{}, &fakeScenarioCatalog{}, 10_000).(*battleService)
	view, err := service.GetFactionView(context.Background(), validID, "a")
	if err != nil {
		t.Fatal(err)
	}
	if len(view.State.Units) != 1 || len(view.State.Buildings) != 1 {
		t.Fatalf("view = %#v", view)
	}
	if _, err := service.GetFactionView(context.Background(), validID, "outsider"); !errors.Is(err, ErrInvalidPerspective) {
		t.Fatalf("error = %v", err)
	}
}

func TestCreateBattlePropagatesDependencyAndPersistenceErrors(t *testing.T) {
	valid := CreateBattleInput{
		ID: "7f22e693-bacd-46bb-b942-64fa613cd388", ScenarioID: "skirmish-v1",
		FactionIDs: []string{"4db8d152-1e04-4d10-8824-859c6ed72878", "41ac3a2f-3371-4de1-b0f6-70600fcd9052"}, TickCount: 100,
	}
	factions := map[string]*faction.Faction{
		valid.FactionIDs[0]: {ID: valid.FactionIDs[0], Name: "A"},
		valid.FactionIDs[1]: {ID: valid.FactionIDs[1], Name: "B"},
	}

	dependencyErr := errors.New("faction database unavailable")
	service := NewService(&fakeBattleRepo{}, &battleFactionRepo{err: dependencyErr}, &fakeScenarioCatalog{}, 10_000)
	if _, err := service.CreateBattle(context.Background(), valid); !errors.Is(err, dependencyErr) {
		t.Fatalf("CreateBattle() error = %v, want dependency error", err)
	}

	for _, persistenceErr := range []error{ErrBattleAlreadyExists, errors.New("battle database unavailable")} {
		repository := &fakeBattleRepo{createErr: persistenceErr}
		service := NewService(repository, &battleFactionRepo{factions: factions}, &fakeScenarioCatalog{resolved: game.Scenario{ID: "castle-siege-v1"}}, 10_000)
		if _, err := service.CreateBattle(context.Background(), valid); !errors.Is(err, persistenceErr) {
			t.Fatalf("CreateBattle() error = %v, want %v", err, persistenceErr)
		}
	}
}

func TestListBattleEventsValidatesAndPropagatesErrors(t *testing.T) {
	validID := "7f22e693-bacd-46bb-b942-64fa613cd388"
	for _, limit := range []int{0, 501} {
		service := NewService(&fakeBattleRepo{}, &battleFactionRepo{}, &fakeScenarioCatalog{}, 10_000)
		if _, err := service.ListBattleEvents(context.Background(), validID, nil, 0, limit, false); !errors.Is(err, ErrInvalidBattle) {
			t.Fatalf("limit %d error = %v, want ErrInvalidBattle", limit, err)
		}
	}
	service := NewService(&fakeBattleRepo{}, &battleFactionRepo{}, &fakeScenarioCatalog{}, 10_000)
	if _, err := service.ListBattleEvents(context.Background(), validID, nil, 1, 50, true); !errors.Is(err, ErrInvalidBattle) {
		t.Fatalf("latest query with cursor error = %v, want ErrInvalidBattle", err)
	}

	for _, attempt := range []int{-1, 3} {
		repository := &fakeBattleRepo{run: &BattleRun{ID: validID, Attempt: 2}}
		service := NewService(repository, &battleFactionRepo{}, &fakeScenarioCatalog{}, 10_000)
		if _, err := service.ListBattleEvents(context.Background(), validID, &attempt, 0, 50, false); !errors.Is(err, ErrInvalidBattle) {
			t.Fatalf("attempt %d error = %v, want ErrInvalidBattle", attempt, err)
		}
	}

	repositoryErr := errors.New("events unavailable")
	repository := &fakeBattleRepo{run: &BattleRun{ID: validID, Attempt: 1}, listErr: repositoryErr}
	service = NewService(repository, &battleFactionRepo{}, &fakeScenarioCatalog{}, 10_000)
	if _, err := service.ListBattleEvents(context.Background(), validID, nil, 0, 50, false); !errors.Is(err, repositoryErr) {
		t.Fatalf("ListBattleEvents() error = %v, want repository error", err)
	}
}

type fakeScenarioCatalog struct {
	resolved   game.Scenario
	digest     string
	err        error
	identities [2]FactionIdentity
	summaries  []ScenarioSummary
}

func (c *fakeScenarioCatalog) Resolve(_ string, identities [2]FactionIdentity) (game.Scenario, string, error) {
	c.identities = identities
	if c.err == nil && c.resolved.ID != "" && c.resolved.Map.Width == 0 {
		c.resolved = serviceTestScenario(identities)
	}
	return c.resolved, c.digest, c.err
}

func (c *fakeScenarioCatalog) Summaries() []ScenarioSummary { return c.summaries }

type battleFactionRepo struct {
	factions map[string]*faction.Faction
	err      error
}

func (r *battleFactionRepo) GetFaction(_ context.Context, id string) (*faction.Faction, error) {
	if r.err != nil {
		return nil, r.err
	}
	stored := r.factions[id]
	if stored == nil {
		return nil, factionservice.ErrFactionNotFound
	}
	copy := *stored
	return &copy, nil
}

type fakeBattleRepo struct {
	created     CreateBattleParams
	createErr   error
	run         *BattleRun
	events      []StoredBattleEvent
	listAttempt int
	listLatest  bool
	getErr      error
	listErr     error
}

func (r *fakeBattleRepo) CreateBattle(_ context.Context, params CreateBattleParams) error {
	r.created = params
	return r.createErr
}
func (r *fakeBattleRepo) GetBattle(context.Context, string) (*BattleRun, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	if r.run == nil {
		return nil, ErrBattleNotFound
	}
	return r.run, nil
}
func (r *fakeBattleRepo) ListBattleEvents(_ context.Context, _ string, attempt int, _ uint64, _ int, latest bool) ([]StoredBattleEvent, error) {
	r.listAttempt = attempt
	r.listLatest = latest
	return r.events, r.listErr
}

func serviceTestScenario(identities [2]FactionIdentity) game.Scenario {
	plan := controller.PlanProposal{Name: "attack", ActionWeights: map[string]int64{game.ActionAttack: 1000}}
	blueprint := game.Blueprint{Units: []game.UnitSelection{{Type: "vanguard", Count: 1}}, RecruitmentPriority: []string{"vanguard"}}
	ruleset := game.Ruleset{ID: "castle-siege-v1", Version: "1", DesignBudget: 120, StartingGold: 60, Units: []game.UnitType{{ID: "vanguard", Cost: 35, Health: 70, Attack: 9, Defense: 7, Speed: 4, Movement: 1, Range: 1, Vision: 2}}, Upgrades: []game.Upgrade{{ID: "arsenal", Cost: 20, AttackBonus: 2}}, Buildings: game.BuildingRules{CastleHealth: 300, CastleDefense: 8, CastleVision: 4, CastleIncome: 10, EconomyCost: 50, EconomyHealth: 60, EconomyDefense: 2, EconomyVision: 2, EconomyIncome: 10, MaximumEconomyCount: 2}, Combat: game.CombatRules{MinimumDamage: 1, DefendAttackPercent: 80, DefendDamageTakenPercent: 70, LootPercent: 50}}
	gameMap := game.Map{Kind: "authored", Width: 15, Height: 11, Rows: []string{"...............", ".......#.......", "..b....#....b..", "...............", ".......#.......", "...............", ".......#.......", "...............", "..b....#....b..", ".......#.......", "..............."}, CastleSpawns: [2]game.Position{{X: 1, Y: 5}, {X: 13, Y: 5}}, EconomySites: [2][]game.Position{{{X: 2, Y: 2}, {X: 2, Y: 8}}, {{X: 12, Y: 2}, {X: 12, Y: 8}}}}
	factions := make([]game.FactionSetup, 2)
	for index, identity := range identities {
		factions[index] = game.FactionSetup{ID: identity.ID, Name: identity.Name, Blueprint: blueprint, FallbackPlan: plan, ScriptedPlans: []controller.PlanProposal{plan}}
	}
	return game.Scenario{ID: "castle-siege-v1", RulesVersion: "1", RulesetID: ruleset.ID, MaximumTicks: 500, DecisionIntervalTicks: 10, DecisionLeadTicks: 2, Map: gameMap, Ruleset: ruleset, Factions: factions}
}
