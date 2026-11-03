// Checks battle validation, frozen participant snapshots, model selection, controls,
// perspectives, and safe decision/event reads.

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai/profile"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/faction"
	factionservice "github.com/twh09/autonomous_battlefield/backend/internal/faction/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
)

var serviceParticipantIDs = [2]string{
	"4db8d152-1e04-4d10-8824-859c6ed72878",
	"41ac3a2f-3371-4de1-b0f6-70600fcd9052",
}

func scriptedParticipants() []ParticipantInput {
	return []ParticipantInput{
		{FactionID: serviceParticipantIDs[0], Controller: game.ControllerConfig{Type: ai.ControllerScripted}},
		{FactionID: serviceParticipantIDs[1], Controller: game.ControllerConfig{Type: ai.ControllerScripted}},
	}
}

func TestCreateBattleResolvesFactionsAndPersistsSnapshot(t *testing.T) {
	battleRepo := &fakeBattleRepo{}
	factions := &battleFactionRepo{factions: map[string]*faction.Faction{
		"4db8d152-1e04-4d10-8824-859c6ed72878": {ID: "4db8d152-1e04-4d10-8824-859c6ed72878", Name: "Northwind"},
		"41ac3a2f-3371-4de1-b0f6-70600fcd9052": {ID: "41ac3a2f-3371-4de1-b0f6-70600fcd9052", Name: "Verdant"},
	}}
	catalog := &fakeScenarioCatalog{resolved: game.Scenario{ID: "castle-siege-v1"}}
	service := NewService(battleRepo, factions, catalog, 10_000, Options{})

	run, err := service.CreateBattle(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), CreateBattleInput{
		ID: "7f22e693-bacd-46bb-b942-64fa613cd388", ScenarioID: "skirmish-v1",
		Participants: scriptedParticipants(), TickCount: 100,
	})
	if err != nil {
		t.Fatalf("CreateBattle() returned an error: %v", err)
	}
	if run.Status != "queued" || battleRepo.created.ScenarioDigest == "" || battleRepo.created.RequestedTicks != 100 || battleRepo.created.ScenarioSnapshot.Factions[0].Controller.Type != "scripted" {
		t.Fatalf("run/created = %#v / %#v", run, battleRepo.created)
	}
	if catalog.identities[0].Name != "Northwind" || catalog.identities[1].Name != "Verdant" {
		t.Fatalf("resolved identities = %#v", catalog.identities)
	}
}

func TestCreateBattleSelectsAllowancesByRole(t *testing.T) {
	for _, tc := range []struct {
		role   authn.Role
		exempt bool
		tester bool
	}{{authn.Admin, true, false}, {authn.Tester, false, true}, {authn.Player, false, false}, {authn.CI, false, false}} {
		battleRepo := &fakeBattleRepo{}
		factions := &battleFactionRepo{factions: map[string]*faction.Faction{
			"4db8d152-1e04-4d10-8824-859c6ed72878": {ID: "4db8d152-1e04-4d10-8824-859c6ed72878", Name: "Northwind"},
			"41ac3a2f-3371-4de1-b0f6-70600fcd9052": {ID: "41ac3a2f-3371-4de1-b0f6-70600fcd9052", Name: "Verdant"},
		}}
		catalog := &fakeScenarioCatalog{resolved: game.Scenario{ID: "castle-siege-v1"}}
		service := NewService(battleRepo, factions, catalog, 10_000, Options{})
		ctx := authn.WithActor(context.Background(), authn.Actor{ID: "actor", Role: tc.role})
		if _, err := service.CreateBattle(ctx, CreateBattleInput{
			ID: "7f22e693-bacd-46bb-b942-64fa613cd388", ScenarioID: "skirmish-v1",
			Participants: scriptedParticipants(), TickCount: 100,
		}); err != nil {
			t.Fatalf("%s: CreateBattle() returned an error: %v", tc.role, err)
		}
		if got := battleRepo.created; got.AdmissionExempt != tc.exempt || got.TesterAllowance != tc.tester {
			t.Fatalf("%s: AdmissionExempt = %v, TesterAllowance = %v; want %v, %v", tc.role, got.AdmissionExempt, got.TesterAllowance, tc.exempt, tc.tester)
		}
	}
}

func TestDecisionBudgetExemptionAppliesOnlyToAdmins(t *testing.T) {
	for _, tc := range []struct {
		role      authn.Role
		requested bool
		want      bool
	}{{authn.Admin, true, true}, {authn.Admin, false, false}, {authn.Tester, true, false}, {authn.Player, true, false}} {
		battleRepo := &fakeBattleRepo{}
		factions := &battleFactionRepo{factions: map[string]*faction.Faction{
			"4db8d152-1e04-4d10-8824-859c6ed72878": {ID: "4db8d152-1e04-4d10-8824-859c6ed72878", Name: "Northwind"},
			"41ac3a2f-3371-4de1-b0f6-70600fcd9052": {ID: "41ac3a2f-3371-4de1-b0f6-70600fcd9052", Name: "Verdant"},
		}}
		service := NewService(battleRepo, factions, &fakeScenarioCatalog{resolved: game.Scenario{ID: "castle-siege-v1"}}, 10_000, Options{})
		ctx := authn.WithActor(context.Background(), authn.Actor{ID: "actor", Role: tc.role})
		if _, err := service.CreateBattle(ctx, CreateBattleInput{
			ID: "7f22e693-bacd-46bb-b942-64fa613cd388", ScenarioID: "skirmish-v1",
			Participants: scriptedParticipants(), TickCount: 100, DecisionBudgetExempt: tc.requested,
		}); err != nil {
			t.Fatalf("%s: CreateBattle() returned an error: %v", tc.role, err)
		}
		if battleRepo.created.DecisionBudgetExempt != tc.want {
			t.Fatalf("%s requested=%v: DecisionBudgetExempt = %v; want %v", tc.role, tc.requested, battleRepo.created.DecisionBudgetExempt, tc.want)
		}
	}
}

func TestValidateBattleChecksWithoutCreating(t *testing.T) {
	battleRepo := &fakeBattleRepo{}
	factions := &battleFactionRepo{factions: map[string]*faction.Faction{
		"4db8d152-1e04-4d10-8824-859c6ed72878": {ID: "4db8d152-1e04-4d10-8824-859c6ed72878", Name: "Northwind"},
		"41ac3a2f-3371-4de1-b0f6-70600fcd9052": {ID: "41ac3a2f-3371-4de1-b0f6-70600fcd9052", Name: "Verdant"},
	}}
	service := NewService(battleRepo, factions, &fakeScenarioCatalog{resolved: game.Scenario{ID: "castle-siege-v1"}}, 10_000, Options{})
	input := CreateBattleInput{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", ScenarioID: "skirmish-v1", Participants: scriptedParticipants(), TickCount: 100}
	if err := service.ValidateBattle(context.Background(), input); err != nil || battleRepo.created.ID != "" {
		t.Fatalf("ValidateBattle() = %v, created %q", err, battleRepo.created.ID)
	}
	input.TickCount = 0
	if err := service.ValidateBattle(context.Background(), input); !errors.Is(err, ErrInvalidBattle) {
		t.Fatalf("invalid ValidateBattle() = %v", err)
	}
}

func TestCreateBattleValidationAndDependencies(t *testing.T) {
	valid := CreateBattleInput{
		ID: "7f22e693-bacd-46bb-b942-64fa613cd388", ScenarioID: "skirmish-v1",
		Participants: scriptedParticipants(), TickCount: 100,
	}
	t.Run("invalid", func(t *testing.T) {
		tests := []struct {
			name   string
			mutate func(*CreateBattleInput)
		}{
			{name: "zero ticks", mutate: func(input *CreateBattleInput) { input.TickCount = 0 }},
			{name: "too many ticks", mutate: func(input *CreateBattleInput) { input.TickCount = 10_001 }},
			{name: "duplicate factions", mutate: func(input *CreateBattleInput) { input.Participants[1].FactionID = input.Participants[0].FactionID }},
			{name: "missing controller type", mutate: func(input *CreateBattleInput) { input.Participants[0].Controller.Type = "" }},
			{name: "malformed ID", mutate: func(input *CreateBattleInput) { input.ID = "bad" }},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				service := NewService(&fakeBattleRepo{}, &battleFactionRepo{}, &fakeScenarioCatalog{}, 10_000, Options{})
				input := valid
				input.Participants = append([]ParticipantInput(nil), valid.Participants...)
				tt.mutate(&input)
				if _, err := service.CreateBattle(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), input); !errors.Is(err, ErrInvalidBattle) {
					t.Fatalf("error = %v, want ErrInvalidBattle", err)
				}
			})
		}
	})
	t.Run("scenario tick limit", func(t *testing.T) {
		factions := &battleFactionRepo{factions: map[string]*faction.Faction{
			valid.Participants[0].FactionID: {ID: valid.Participants[0].FactionID, Name: "A"},
			valid.Participants[1].FactionID: {ID: valid.Participants[1].FactionID, Name: "B"},
		}}
		catalog := &fakeScenarioCatalog{resolved: serviceTestScenario([2]FactionIdentity{{ID: valid.Participants[0].FactionID, Name: "A"}, {ID: valid.Participants[1].FactionID, Name: "B"}})}
		service := NewService(&fakeBattleRepo{}, factions, catalog, 10_000, Options{})
		input := valid
		input.TickCount = 501
		if _, err := service.CreateBattle(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), input); !errors.Is(err, ErrInvalidBattle) {
			t.Fatalf("error = %v, want ErrInvalidBattle", err)
		}
	})
	t.Run("missing faction", func(t *testing.T) {
		service := NewService(&fakeBattleRepo{}, &battleFactionRepo{}, &fakeScenarioCatalog{}, 10_000, Options{})
		if _, err := service.CreateBattle(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), valid); !errors.Is(err, ErrBattleFactionMissing) {
			t.Fatalf("error = %v, want ErrBattleFactionMissing", err)
		}
	})
	t.Run("missing scenario", func(t *testing.T) {
		factions := &battleFactionRepo{factions: map[string]*faction.Faction{
			valid.Participants[0].FactionID: {ID: valid.Participants[0].FactionID, Name: "A"}, valid.Participants[1].FactionID: {ID: valid.Participants[1].FactionID, Name: "B"},
		}}
		service := NewService(&fakeBattleRepo{}, factions, &fakeScenarioCatalog{err: ErrScenarioNotFound}, 10_000, Options{})
		if _, err := service.CreateBattle(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), valid); !errors.Is(err, ErrScenarioNotFound) {
			t.Fatalf("error = %v, want ErrScenarioNotFound", err)
		}
	})
}

func TestCreateBattlePersistsAllowedModelProfile(t *testing.T) {
	ids := [2]string{"4db8d152-1e04-4d10-8824-859c6ed72878", "41ac3a2f-3371-4de1-b0f6-70600fcd9052"}
	identities := [2]FactionIdentity{{ID: ids[0], Name: "A"}, {ID: ids[1], Name: "B"}}
	factions := &battleFactionRepo{factions: map[string]*faction.Faction{ids[0]: {ID: ids[0], Name: "A"}, ids[1]: {ID: ids[1], Name: "B"}}}
	registry := profile.NewRegistry("cluster", []ai.ModelProfile{{ID: "gemini", DisplayName: "Gemini", Provider: "gemini", Runtime: "api", Scope: "cluster", Enabled: true, MaxSteps: 3, MaxOutputTokens: 100}})
	repository := &fakeBattleRepo{}
	service := NewService(repository, factions, &fakeScenarioCatalog{resolved: serviceTestScenario(identities)}, 10_000, Options{Profiles: registry})
	_, err := service.CreateBattle(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), CreateBattleInput{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", ScenarioID: "castle-siege-v1", TickCount: 10, Participants: []ParticipantInput{{FactionID: ids[0], Controller: game.ControllerConfig{Type: "model", ProfileID: "gemini"}}, {FactionID: ids[1], Controller: game.ControllerConfig{Type: "scripted"}}}})
	if err != nil {
		t.Fatal(err)
	}
	selected := repository.created.ScenarioSnapshot.Factions[0].Controller
	if selected.ProfileID != "gemini" || selected.Provider != "gemini" || selected.Runtime != "api" {
		t.Fatalf("controller=%#v", selected)
	}
	if len(service.(ModelProfileService).ListModelProfiles()) != 1 {
		t.Fatal("profile list missing")
	}
	bad := CreateBattleInput{ID: "8f22e693-bacd-46bb-b942-64fa613cd388", ScenarioID: "castle-siege-v1", TickCount: 10, Participants: []ParticipantInput{{FactionID: ids[0], Controller: game.ControllerConfig{Type: "model", ProfileID: "unknown"}}, {FactionID: ids[1], Controller: game.ControllerConfig{Type: "scripted"}}}}
	if _, err := service.CreateBattle(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), bad); !errors.Is(err, ErrModelProfileUnavailable) {
		t.Fatal(err)
	}
}

func TestPlayerCLIBattleSelectionFollowsAdminPolicy(t *testing.T) {
	t.Setenv("CLI_AVAILABLE", "true")
	ids := serviceParticipantIDs
	identities := [2]FactionIdentity{{ID: ids[0], Name: "A"}, {ID: ids[1], Name: "B"}}
	factions := &battleFactionRepo{factions: map[string]*faction.Faction{
		ids[0]: {ID: ids[0], Name: "A"},
		ids[1]: {ID: ids[1], Name: "B"},
	}}
	policy := profile.AccessPolicy{Profiles: map[string]profile.Access{
		"cli": {Enabled: true, AllowedRoles: []string{"admin", "tester", "player"}},
	}}
	base := profile.NewCatalog("cluster", []ai.ModelProfile{{
		ID: "cli", DisplayName: "CLI", Provider: "codex-cli", Runtime: "cli", Scope: "local", Enabled: true,
		AvailabilityEnvironmentVariable: "CLI_AVAILABLE", SupportedVersionPrefix: "1.",
	}})
	profiles := base.WithAccessPolicy(func() (profile.AccessPolicy, error) { return policy, nil })
	repository := &fakeBattleRepo{}
	service := NewService(repository, factions, &fakeScenarioCatalog{resolved: serviceTestScenario(identities)}, 10_000, Options{Profiles: profiles})
	ctx := authn.WithActor(context.Background(), authn.Actor{ID: "friend", Role: authn.Player})
	input := CreateBattleInput{
		ID: "7f22e693-bacd-46bb-b942-64fa613cd388", ScenarioID: "castle-siege-v1", TickCount: 10,
		Participants: []ParticipantInput{
			{FactionID: ids[0], Controller: game.ControllerConfig{Type: ai.ControllerModel, ProfileID: "cli"}},
			{FactionID: ids[1], Controller: game.ControllerConfig{Type: ai.ControllerScripted}},
		},
	}
	if _, err := service.CreateBattle(ctx, input); err != nil {
		t.Fatalf("approved Player CLI battle rejected: %v", err)
	}
	if selected := repository.created.ScenarioSnapshot.Factions[0].Controller; selected.ProfileID != "cli" || selected.Runtime != "cli" {
		t.Fatalf("controller=%#v", selected)
	}

	policy.Profiles["cli"] = profile.Access{Enabled: true, AllowedRoles: []string{"admin", "tester"}}
	input.ID = "8f22e693-bacd-46bb-b942-64fa613cd388"
	if _, err := service.CreateBattle(ctx, input); !errors.Is(err, authn.ErrForbidden) {
		t.Fatalf("revoked Player CLI battle error = %v, want permission denied", err)
	}
}

func TestListBattleEventsDefaultsToCurrentAttempt(t *testing.T) {
	repo := &fakeBattleRepo{
		run:    &BattleRun{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", Attempt: 2},
		events: []StoredBattleEvent{{Attempt: 2, Sequence: 5}},
	}
	service := NewService(repo, &battleFactionRepo{}, &fakeScenarioCatalog{}, 10_000, Options{})
	page, err := service.ListBattleEvents(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), repo.run.ID, nil, 0, 100, true)
	if err != nil {
		t.Fatalf("ListBattleEvents() returned an error: %v", err)
	}
	if page.Attempt != 2 || page.NextCursor == nil || *page.NextCursor != 5 || repo.listAttempt != 2 || !repo.listLatest {
		t.Fatalf("page = %#v, queried attempt = %d", page, repo.listAttempt)
	}
}

func TestGetBattleRejectsMalformedID(t *testing.T) {
	service := NewService(&fakeBattleRepo{}, &battleFactionRepo{}, &fakeScenarioCatalog{}, 10_000, Options{})
	if _, err := service.GetBattle(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), "not-a-uuid"); !errors.Is(err, ErrInvalidBattle) {
		t.Fatalf("GetBattle() error = %v, want ErrInvalidBattle", err)
	}
}

func TestBattleServiceDelegatesCatalogAndRepositoryReads(t *testing.T) {
	validID := "7f22e693-bacd-46bb-b942-64fa613cd388"
	catalog := &fakeScenarioCatalog{summaries: []ScenarioSummary{{ID: "skirmish-v1"}}}
	repository := &fakeBattleRepo{run: &BattleRun{ID: validID}}
	service := NewService(repository, &battleFactionRepo{}, catalog, 10_000, Options{})
	if summaries := service.ListScenarios(); len(summaries) != 1 || summaries[0].ID != "skirmish-v1" {
		t.Fatalf("ListScenarios() = %#v", summaries)
	}
	run, err := service.GetBattle(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), validID)
	if err != nil || run.ID != validID {
		t.Fatalf("GetBattle() = %#v, %v", run, err)
	}

	repository.getErr = errors.New("database unavailable")
	if _, err := service.GetBattle(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), validID); err == nil {
		t.Fatal("GetBattle() returned no repository error")
	}
}

func TestStopBattleValidatesAndReturnsStoppedRun(t *testing.T) {
	validID := "7f22e693-bacd-46bb-b942-64fa613cd388"
	repository := &fakeBattleRepo{run: &BattleRun{ID: validID, Status: "stopped"}}
	service := NewService(repository, &battleFactionRepo{}, &fakeScenarioCatalog{}, 10_000, Options{})
	run, err := service.StopBattle(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), validID)
	if err != nil || run.Status != "stopped" || repository.stoppedID != validID {
		t.Fatalf("run=%#v stoppedID=%q err=%v", run, repository.stoppedID, err)
	}
	if _, err := service.StopBattle(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), "bad"); !errors.Is(err, ErrInvalidBattle) {
		t.Fatalf("malformed ID error = %v", err)
	}
	repository.stopErr = ErrBattleNotActive
	if _, err := service.StopBattle(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), validID); !errors.Is(err, ErrBattleNotActive) {
		t.Fatalf("inactive error = %v", err)
	}
}

func TestPauseAndResumeBattleValidateAndReturnRun(t *testing.T) {
	validID := "7f22e693-bacd-46bb-b942-64fa613cd388"
	repository := &fakeBattleRepo{run: &BattleRun{ID: validID, Status: "running"}}
	service := NewService(repository, &battleFactionRepo{}, &fakeScenarioCatalog{}, 10_000, Options{})

	repository.run.Paused = true
	paused, err := service.PauseBattle(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), validID)
	if err != nil || !paused.Paused || repository.pausedID != validID || !repository.pausedValue {
		t.Fatalf("paused=%#v repository=%#v err=%v", paused, repository, err)
	}
	repository.run.Paused = false
	resumed, err := service.ResumeBattle(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), validID)
	if err != nil || resumed.Paused || repository.pausedValue {
		t.Fatalf("resumed=%#v repository=%#v err=%v", resumed, repository, err)
	}
	if _, err := service.PauseBattle(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), "bad"); !errors.Is(err, ErrInvalidBattle) {
		t.Fatalf("malformed ID error = %v", err)
	}
}

type historyBattleRepo struct {
	*fakeBattleRepo
	records    []ai.DecisionRecord
	historyErr error
}

func (r *historyBattleRepo) ListBattleDecisions(context.Context, string, int) ([]ai.DecisionRecord, error) {
	return r.records, r.historyErr
}

func TestDecisionHistoryIsSafeAndValidated(t *testing.T) {
	repository := &historyBattleRepo{fakeBattleRepo: &fakeBattleRepo{}, records: []ai.DecisionRecord{{ID: "request", FactionID: "faction", TargetTick: 10, Status: "accepted", ProfileID: "profile", Provider: "openai", Runtime: "api", ModelIdentifier: "model", ProviderMetadata: map[string]any{"contextDelivery": "prefetch"}, Usage: ai.Usage{Requests: 1}}}}
	service := NewService(repository, &battleFactionRepo{}, &fakeScenarioCatalog{}, 10_000, Options{}).(DecisionHistoryService)
	history, err := service.ListDecisionHistory(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), "7f22e693-bacd-46bb-b942-64fa613cd388", 1)
	if err != nil || len(history) != 1 || history[0].ProfileID != "profile" || history[0].Usage.Requests != 1 || history[0].ContextDelivery != "prefetch" {
		t.Fatalf("history=%#v err=%v", history, err)
	}
	if _, err := service.ListDecisionHistory(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), "bad", 0); !errors.Is(err, ErrInvalidBattle) {
		t.Fatal(err)
	}
	repository.historyErr = errors.New("database")
	if _, err := service.ListDecisionHistory(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), "7f22e693-bacd-46bb-b942-64fa613cd388", 1); err == nil {
		t.Fatal("history error ignored")
	}
}

func TestFactionViewShowsAllPersistedEntities(t *testing.T) {
	validID := "7f22e693-bacd-46bb-b942-64fa613cd388"
	state := game.State{Factions: []game.FactionState{{FactionID: "a"}, {FactionID: "b"}}, Units: []game.Unit{{ID: "a-unit", FactionID: "a", Position: game.Position{X: 1, Y: 1}, Health: 10, Vision: 1}, {ID: "b-unit", FactionID: "b", Position: game.Position{X: 12, Y: 9}, Health: 10}}, Buildings: []game.Building{{ID: "b-castle", FactionID: "b", Type: game.BuildingCastle, Position: game.Position{X: 13, Y: 5}, Health: 300}}}
	repository := &fakeBattleRepo{run: &BattleRun{ID: validID, ScenarioSnapshot: game.Scenario{Map: serviceTestScenario([2]FactionIdentity{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}}).Map}, CurrentState: state}}
	service := NewService(repository, &battleFactionRepo{}, &fakeScenarioCatalog{}, 10_000, Options{}).(*battleService)
	view, err := service.GetFactionView(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), validID, "a")
	if err != nil {
		t.Fatal(err)
	}
	if len(view.State.Units) != 2 || len(view.State.Buildings) != 1 || len(view.State.Factions) != 1 {
		t.Fatalf("view = %#v", view)
	}
	if _, err := service.GetFactionView(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), validID, "outsider"); !errors.Is(err, ErrInvalidPerspective) {
		t.Fatalf("error = %v", err)
	}
}

func TestCreateBattlePropagatesDependencyAndPersistenceErrors(t *testing.T) {
	valid := CreateBattleInput{
		ID: "7f22e693-bacd-46bb-b942-64fa613cd388", ScenarioID: "skirmish-v1",
		Participants: scriptedParticipants(), TickCount: 100,
	}
	factions := map[string]*faction.Faction{
		valid.Participants[0].FactionID: {ID: valid.Participants[0].FactionID, Name: "A"},
		valid.Participants[1].FactionID: {ID: valid.Participants[1].FactionID, Name: "B"},
	}

	dependencyErr := errors.New("faction database unavailable")
	service := NewService(&fakeBattleRepo{}, &battleFactionRepo{err: dependencyErr}, &fakeScenarioCatalog{}, 10_000, Options{})
	if _, err := service.CreateBattle(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), valid); !errors.Is(err, dependencyErr) {
		t.Fatalf("CreateBattle() error = %v, want dependency error", err)
	}

	for _, persistenceErr := range []error{ErrBattleAlreadyExists, errors.New("battle database unavailable")} {
		repository := &fakeBattleRepo{createErr: persistenceErr}
		service := NewService(repository, &battleFactionRepo{factions: factions}, &fakeScenarioCatalog{resolved: game.Scenario{ID: "castle-siege-v1"}}, 10_000, Options{})
		if _, err := service.CreateBattle(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), valid); !errors.Is(err, persistenceErr) {
			t.Fatalf("CreateBattle() error = %v, want %v", err, persistenceErr)
		}
	}
}

func TestCreateBattleRetryReplaysOrConflicts(t *testing.T) {
	input := CreateBattleInput{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", ScenarioID: "skirmish-v1", Participants: scriptedParticipants(), TickCount: 100}
	factions := map[string]*faction.Faction{
		input.Participants[0].FactionID: {ID: input.Participants[0].FactionID, Name: "A"},
		input.Participants[1].FactionID: {ID: input.Participants[1].FactionID, Name: "B"},
	}
	catalog := &fakeScenarioCatalog{resolved: game.Scenario{ID: "castle-siege-v1"}}
	ctx := authn.WithActor(context.Background(), authn.Actor{ID: "owner", Role: authn.Player})
	first := &fakeBattleRepo{}
	if _, err := NewService(first, &battleFactionRepo{factions: factions}, catalog, 10_000, Options{}).CreateBattle(ctx, input); err != nil {
		t.Fatal(err)
	}
	stored := BattleRun{ID: input.ID, OwnerID: "owner", ScenarioDigest: first.created.ScenarioDigest, RequestedTicks: 100, Status: "running"}
	lookupErr := errors.New("battle database unavailable")
	tests := []struct {
		name    string
		run     *BattleRun
		getErr  error
		mutate  func(*BattleRun)
		wantErr error
	}{
		{name: "identical retry replays", run: &stored},
		{name: "other caller conflicts", run: &stored, mutate: func(r *BattleRun) { r.OwnerID = "other" }, wantErr: ErrBattleAlreadyExists},
		{name: "different tick count conflicts", run: &stored, mutate: func(r *BattleRun) { r.RequestedTicks = 50 }, wantErr: ErrBattleAlreadyExists},
		{name: "different scenario conflicts", run: &stored, mutate: func(r *BattleRun) { r.ScenarioDigest = "other" }, wantErr: ErrBattleAlreadyExists},
		{name: "deleted before lookup conflicts", wantErr: ErrBattleAlreadyExists},
		{name: "lookup failure surfaces", getErr: lookupErr, wantErr: lookupErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repository := &fakeBattleRepo{createErr: ErrBattleAlreadyExists, getErr: tt.getErr}
			if tt.run != nil {
				run := *tt.run
				if tt.mutate != nil {
					tt.mutate(&run)
				}
				repository.run = &run
			}
			got, err := NewService(repository, &battleFactionRepo{factions: factions}, catalog, 10_000, Options{}).CreateBattle(ctx, input)
			if !errors.Is(err, tt.wantErr) || (tt.wantErr == nil && (got == nil || got.ID != input.ID)) {
				t.Fatalf("CreateBattle() = %v, %v; want error %v", got, err, tt.wantErr)
			}
		})
	}
}

func TestListBattleEventsValidatesAndPropagatesErrors(t *testing.T) {
	validID := "7f22e693-bacd-46bb-b942-64fa613cd388"
	for _, limit := range []int{0, 501} {
		service := NewService(&fakeBattleRepo{}, &battleFactionRepo{}, &fakeScenarioCatalog{}, 10_000, Options{})
		if _, err := service.ListBattleEvents(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), validID, nil, 0, limit, false); !errors.Is(err, ErrInvalidBattle) {
			t.Fatalf("limit %d error = %v, want ErrInvalidBattle", limit, err)
		}
	}
	service := NewService(&fakeBattleRepo{}, &battleFactionRepo{}, &fakeScenarioCatalog{}, 10_000, Options{})
	if _, err := service.ListBattleEvents(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), validID, nil, 1, 50, true); !errors.Is(err, ErrInvalidBattle) {
		t.Fatalf("latest query with cursor error = %v, want ErrInvalidBattle", err)
	}

	for _, attempt := range []int{-1, 3} {
		repository := &fakeBattleRepo{run: &BattleRun{ID: validID, Attempt: 2}}
		service := NewService(repository, &battleFactionRepo{}, &fakeScenarioCatalog{}, 10_000, Options{})
		if _, err := service.ListBattleEvents(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), validID, &attempt, 0, 50, false); !errors.Is(err, ErrInvalidBattle) {
			t.Fatalf("attempt %d error = %v, want ErrInvalidBattle", attempt, err)
		}
	}

	repositoryErr := errors.New("events unavailable")
	repository := &fakeBattleRepo{run: &BattleRun{ID: validID, Attempt: 1}, listErr: repositoryErr}
	service = NewService(repository, &battleFactionRepo{}, &fakeScenarioCatalog{}, 10_000, Options{})
	if _, err := service.ListBattleEvents(authn.WithActor(context.Background(), authn.Actor{ID: "test-admin", Role: authn.Admin}), validID, nil, 0, 50, false); !errors.Is(err, repositoryErr) {
		t.Fatalf("ListBattleEvents() error = %v, want repository error", err)
	}
}

type fakeScenarioCatalog struct {
	resolved   game.Scenario
	err        error
	identities [2]FactionIdentity
	summaries  []ScenarioSummary
}

func (c *fakeScenarioCatalog) Resolve(_ string, identities [2]FactionIdentity) (game.Scenario, error) {
	c.identities = identities
	if c.err == nil && c.resolved.ID != "" && c.resolved.Map.Width == 0 {
		c.resolved = serviceTestScenario(identities)
	}
	return c.resolved, c.err
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
	summaries    []BattleSummary
	historyQuery BattleHistoryQuery
	versions     []string
	created      CreateBattleParams
	createErr    error
	run          *BattleRun
	events       []StoredBattleEvent
	listAttempt  int
	listLatest   bool
	getErr       error
	listErr      error
	stopErr      error
	stoppedID    string
	pauseErr     error
	pausedID     string
	pausedValue  bool
	frames       []StoredReplayFrame
	frameErr     error
	frameAttempt int
	allowance    Allowance
	allowanceErr error
	// allowanceOwner and allowanceTester record the last BattleAllowance call.
	allowanceOwner  string
	allowanceTester bool
}

func (r *fakeBattleRepo) CreateBattle(_ context.Context, params CreateBattleParams) error {
	r.created = params
	return r.createErr
}
func (r *fakeBattleRepo) BattleAllowance(_ context.Context, owner string, tester bool) (Allowance, error) {
	r.allowanceOwner, r.allowanceTester = owner, tester
	return r.allowance, r.allowanceErr
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
func (r *fakeBattleRepo) StopBattle(_ context.Context, id string) error {
	r.stoppedID = id
	return r.stopErr
}
func (r *fakeBattleRepo) SetBattlePaused(_ context.Context, id string, paused bool) error {
	r.pausedID = id
	r.pausedValue = paused
	return r.pauseErr
}

// The base fake stores no decisions; tests that need them embed it and override these.
func (r *fakeBattleRepo) ListBattleDecisions(context.Context, string, int) ([]ai.DecisionRecord, error) {
	return nil, nil
}
func (r *fakeBattleRepo) GetDecision(context.Context, string) (ai.DecisionRecord, error) {
	return ai.DecisionRecord{}, ai.ErrDecisionNotFound
}

// GetReplayFrame returns the latest stored frame at or before tick, like the database.
func (r *fakeBattleRepo) GetReplayFrame(_ context.Context, _ string, attempt int, tick uint64) (StoredReplayFrame, error) {
	r.frameAttempt = attempt
	if r.frameErr != nil {
		return StoredReplayFrame{}, r.frameErr
	}
	for index := len(r.frames) - 1; index >= 0; index-- {
		if r.frames[index].Tick <= tick {
			return r.frames[index], nil
		}
	}
	return StoredReplayFrame{}, ErrReplayUnavailable
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
	return game.Scenario{ID: "castle-siege-v1", RulesVersion: "1", RulesetID: ruleset.ID, MaximumTicks: 500, DecisionIntervalTicks: 10, Map: gameMap, Ruleset: ruleset, Factions: factions}
}

func (r *fakeBattleRepo) ListBattles(_ context.Context, query BattleHistoryQuery) ([]BattleSummary, error) {
	r.historyQuery = query
	return r.summaries, r.listErr
}

func (r *fakeBattleRepo) ListRulesVersions(context.Context) ([]string, error) {
	return r.versions, r.listErr
}

func TestBattleControlOwnershipAllRoles(t *testing.T) {
	for _, role := range []authn.Role{authn.Player, authn.Tester, authn.Admin} {
		for _, owner := range []string{"self", "other", authn.LegacyOwner} {
			for _, operation := range []string{"stop", "pause", "resume"} {
				t.Run(string(role)+owner+operation, func(t *testing.T) {
					checkBattleOwnership(t, role, owner, operation)
				})
			}
		}
	}
}

func checkBattleOwnership(t *testing.T, role authn.Role, owner, operation string) {
	t.Helper()
	const id = "7f22e693-bacd-46bb-b942-64fa613cd388"
	repo := &fakeBattleRepo{run: &BattleRun{ID: id, OwnerID: owner, Status: "running"}}
	svc := NewService(repo, nil, nil, 100, Options{})
	ctx := authn.WithActor(context.Background(), authn.Actor{ID: "self", Role: role})
	var e error
	switch operation {
	case "stop":
		_, e = svc.StopBattle(ctx, id)
	case "pause":
		_, e = svc.PauseBattle(ctx, id)
	case "resume":
		_, e = svc.ResumeBattle(ctx, id)
	}
	allowed := role == authn.Admin || owner == "self"
	if (e == nil) != allowed || (!allowed && (repo.stoppedID != "" || repo.pausedID != "")) {
		t.Fatalf("control %s: %v", operation, e)
	}
}

func TestControllerSelectionFreezesModelAndEffort(t *testing.T) {
	t.Setenv("SELECTION_CLI_AVAILABLE", "true")
	registry := profile.NewCatalog("cluster", []ai.ModelProfile{{ID: "cli", DisplayName: "CLI", Provider: "codex-cli", Runtime: "cli", Scope: "local", Enabled: true, Model: "fast", SupportedVersionPrefix: "codex-cli 0.155.", AvailabilityEnvironmentVariable: "SELECTION_CLI_AVAILABLE", ModelOptions: []ai.ModelOption{{ID: "fast", ReasoningEfforts: []string{"low"}}, {ID: "deep", ReasoningEfforts: []string{"high"}}}}})
	s := &battleService{profiles: registry}
	selected, err := s.resolveController(context.Background(), game.ControllerConfig{Type: "model", ProfileID: "cli", ModelIdentifier: "deep", ReasoningEffort: "high"})
	if err != nil || selected.ModelIdentifier != "deep" || selected.ReasoningEffort != "high" || selected.Provider != "codex-cli" {
		t.Fatal(selected, err)
	}
	if _, err = s.resolveController(authn.WithActor(context.Background(), authn.Actor{ID: "player", Role: authn.Player}), selected); !errors.Is(err, authn.ErrForbidden) {
		t.Fatal("selection bypassed profile permissions", err)
	}
	for _, selection := range []game.ControllerConfig{
		{Type: "model", ProfileID: "cli", ModelIdentifier: "unknown"},
		{Type: "model", ProfileID: "cli", ModelIdentifier: "deep", ReasoningEffort: "low"},
		{Type: "scripted", ModelIdentifier: "deep"},
		{Type: "scripted", ReasoningEffort: "low"},
	} {
		if _, err := s.resolveController(context.Background(), selection); err == nil {
			t.Fatal("invalid selection accepted", selection)
		}
	}
}

func TestModelRolesGateBattleSelection(t *testing.T) {
	t.Setenv("MODEL_ROLES_CLI_AVAILABLE", "true")
	ids := serviceParticipantIDs
	identities := [2]FactionIdentity{{ID: ids[0], Name: "A"}, {ID: ids[1], Name: "B"}}
	factions := &battleFactionRepo{factions: map[string]*faction.Faction{ids[0]: {ID: ids[0], Name: "A"}, ids[1]: {ID: ids[1], Name: "B"}}}
	base := profile.NewCatalog("cluster", []ai.ModelProfile{{
		ID: "cli", DisplayName: "CLI", Provider: "claude-cli", Runtime: "cli", Scope: "local", Enabled: true, Model: "opus",
		AvailabilityEnvironmentVariable: "MODEL_ROLES_CLI_AVAILABLE", SupportedVersionPrefix: "2.",
		ModelOptions: []ai.ModelOption{{ID: "haiku"}, {ID: "opus", ReasoningEfforts: []string{"low"}}},
	}})
	policy := profile.AccessPolicy{Profiles: map[string]profile.Access{"cli": {Enabled: true, AllowedRoles: []string{"admin", "tester", "player"}, Models: map[string]profile.ModelAccess{
		"opus": {AllowedRoles: []string{"tester"}},
	}}}}
	repository := &fakeBattleRepo{}
	service := NewService(repository, factions, &fakeScenarioCatalog{resolved: serviceTestScenario(identities)}, 10_000, Options{Profiles: base.WithAccessPolicy(func() (profile.AccessPolicy, error) { return policy, nil })})
	input := func(model string) CreateBattleInput {
		return CreateBattleInput{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", ScenarioID: "castle-siege-v1", TickCount: 10, Participants: []ParticipantInput{
			{FactionID: ids[0], Controller: game.ControllerConfig{Type: ai.ControllerModel, ProfileID: "cli", ModelIdentifier: model}},
			{FactionID: ids[1], Controller: game.ControllerConfig{Type: ai.ControllerScripted}},
		}}
	}
	as := func(role authn.Role) context.Context {
		return authn.WithActor(context.Background(), authn.Actor{ID: string(role), Role: role})
	}
	for _, tt := range []struct {
		role  authn.Role
		model string
		want  string
	}{
		{authn.Player, "haiku", "haiku"},
		{authn.Player, "", "haiku"},
		{authn.Tester, "opus", "opus"},
		{authn.Tester, "", "opus"},
	} {
		if _, err := service.CreateBattle(as(tt.role), input(tt.model)); err != nil {
			t.Fatalf("%s selecting %q: %v", tt.role, tt.model, err)
		}
		if got := repository.created.ScenarioSnapshot.Factions[0].Controller.ModelIdentifier; got != tt.want {
			t.Fatalf("%s selecting %q froze %q, want %q", tt.role, tt.model, got, tt.want)
		}
	}
	for _, role := range []authn.Role{authn.Player, authn.Admin} {
		if err := service.ValidateBattle(as(role), input("opus")); !errors.Is(err, authn.ErrForbidden) {
			t.Fatalf("%s selecting a disallowed model: %v, want permission denied", role, err)
		}
	}
	if err := service.ValidateBattle(as(authn.Player), input("unconfigured")); !errors.Is(err, ErrModelProfileUnavailable) {
		t.Fatalf("unconfigured model: %v, want unavailable", err)
	}
	policy.Profiles["cli"] = profile.Access{Enabled: true, AllowedRoles: []string{"admin", "tester", "player"}, Models: map[string]profile.ModelAccess{
		"haiku": {AllowedRoles: []string{"tester"}}, "opus": {AllowedRoles: []string{"tester"}},
	}}
	if err := service.ValidateBattle(as(authn.Player), input("")); !errors.Is(err, authn.ErrForbidden) {
		t.Fatalf("player without an allowed model: %v, want permission denied", err)
	}
}
