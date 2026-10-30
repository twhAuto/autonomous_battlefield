// Checks league creation, permissions, idempotent retries, reads and cancellation against
// in-memory storage and battle fakes.

package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/faction"
	factionservice "github.com/twh09/autonomous_battlefield/backend/internal/faction/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/league"
)

const leagueID = "9d7a1d2c-6c43-4cf8-9a6e-0c5a1f3b2e10"

var errDatabase = errors.New("database unavailable")

func factionID(i int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", i) }

func actorContext(role authn.Role) context.Context {
	return authn.WithActor(context.Background(), authn.Actor{ID: "actor-" + string(role), Role: role})
}

func validInput(entrants int) CreateInput {
	input := CreateInput{ID: leagueID, Name: "  Autumn cup ", ScenarioIDs: []string{"frontier-siege", "castle-siege"}, TickCount: 500}
	for i := range entrants {
		controller := game.ControllerConfig{Type: "scripted"}
		if i%2 == 0 {
			controller = game.ControllerConfig{Type: "model", ProfileID: "claude-subscription-local", ModelIdentifier: "sonnet", ReasoningEffort: "low"}
		}
		input.Entrants = append(input.Entrants, EntrantInput{FactionID: strings.ToUpper(factionID(i)), Group: i % (entrants / league.GroupSize), Controller: controller})
	}
	return input
}

type fixtureHarness struct {
	repo     *memoryRepo
	battles  *fakeBattles
	factions *fakeFactions
	service  *Service
}

func newHarness() *fixtureHarness { return newHarnessWith(Options{}) }

// newHarnessWith builds a harness whose battles record their CLI sides through the
// options' profiles.
func newHarnessWith(options Options) *fixtureHarness {
	repo := &memoryRepo{leagues: map[string]*league.League{}, battles: map[string]string{}, origins: map[string]BattleProvenance{}, cliSides: map[string]int{}, cliSlots: 4}
	battles := &fakeBattles{repo: repo, profiles: options.Profiles}
	factions := &fakeFactions{}
	return &fixtureHarness{repo: repo, battles: battles, factions: factions, service: New(repo, battles, factions, options)}
}

// fakeProfiles maps profile IDs to their runtime.
type fakeProfiles map[string]string

func (p fakeProfiles) Get(id string) (ai.ModelProfile, bool) {
	runtime, ok := p[id]
	return ai.ModelProfile{ID: id, Runtime: runtime}, ok
}

func TestCreateLeagueValidatesEveryEntrantOnEveryMap(t *testing.T) {
	h := newHarness()
	created, err := h.service.CreateLeague(actorContext(authn.Admin), validInput(8))
	if err != nil {
		t.Fatal(err)
	}
	if created.Name != "Autumn cup" || created.OwnerID != "actor-admin" || created.GroupCount != 2 || len(created.Fixtures) != 4 || created.Status != league.StatusRunning {
		t.Fatalf("league = %+v", created)
	}
	if created.Entrants[3].FactionName != "Faction 3" || created.Entrants[3].FactionID != factionID(3) || created.Entrants[3].Group != 1 {
		t.Fatalf("entrant = %+v", created.Entrants[3])
	}
	if len(h.battles.validated) != 16 {
		t.Fatalf("validated %d battles, want 8 entrants on 2 maps", len(h.battles.validated))
	}
	seen := map[string]int{}
	for _, input := range h.battles.validated {
		if input.TickCount != 500 || input.Participants[0].FactionID == input.Participants[1].FactionID || input.DecisionBudgetExempt {
			t.Fatalf("validation input = %+v", input)
		}
		seen[input.Participants[0].FactionID]++
		seen[input.Participants[1].FactionID]++
	}
	if seen[factionID(7)] != 4 {
		t.Fatalf("entrant 7 validated %d times, want both sides on both maps", seen[factionID(7)])
	}
}

func TestCreateLeagueRejectsInvalidShapes(t *testing.T) {
	cases := map[string]func(*CreateInput){
		"seven entrants":     func(in *CreateInput) { in.Entrants = in.Entrants[:7] },
		"twelve entrants":    func(in *CreateInput) { in.Entrants = append(in.Entrants, validInput(4).Entrants...) },
		"uneven groups":      func(in *CreateInput) { in.Entrants[0].Group = 1 },
		"group out of range": func(in *CreateInput) { in.Entrants[0].Group = 2 },
		"negative group":     func(in *CreateInput) { in.Entrants[0].Group = -1 },
		"duplicate faction":  func(in *CreateInput) { in.Entrants[2].FactionID = in.Entrants[0].FactionID },
		"bad faction ID":     func(in *CreateInput) { in.Entrants[0].FactionID = "faction" },
		"bad league ID":      func(in *CreateInput) { in.ID = "league" },
		"blank name":         func(in *CreateInput) { in.Name = "   " },
		"long name":          func(in *CreateInput) { in.Name = strings.Repeat("x", 81) },
		"no maps":            func(in *CreateInput) { in.ScenarioIDs = nil },
		"duplicate maps":     func(in *CreateInput) { in.ScenarioIDs = []string{"a", "a"} },
		"blank map":          func(in *CreateInput) { in.ScenarioIDs = []string{" "} },
		"too many maps":      func(in *CreateInput) { in.ScenarioIDs = []string{"a", "b", "c", "d", "e", "f"} },
		"zero ticks":         func(in *CreateInput) { in.TickCount = 0 },
	}
	for name, mutate := range cases {
		h := newHarness()
		input := validInput(8)
		mutate(&input)
		if _, err := h.service.CreateLeague(actorContext(authn.Admin), input); !errors.Is(err, ErrInvalidLeague) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := newHarness().service.CreateLeague(actorContext(authn.Admin), validInput(16)); err != nil {
		t.Fatalf("16 entrants: %v", err)
	}
}

func TestCreateLeagueRequiresAdminAndSurfacesDependencyErrors(t *testing.T) {
	for _, role := range []authn.Role{authn.Developer, authn.Player, authn.CI} {
		if _, err := newHarness().service.CreateLeague(actorContext(role), validInput(8)); !errors.Is(err, authn.ErrForbidden) {
			t.Errorf("%s: err = %v", role, err)
		}
	}
	if _, err := newHarness().service.CreateLeague(context.Background(), validInput(8)); !errors.Is(err, authn.ErrForbidden) {
		t.Errorf("anonymous: err = %v", err)
	}
	h := newHarness()
	h.battles.validateErr = battleservice.ErrModelProfileUnavailable
	if _, err := h.service.CreateLeague(actorContext(authn.Admin), validInput(8)); !errors.Is(err, battleservice.ErrModelProfileUnavailable) {
		t.Fatalf("validation err = %v", err)
	}
	h = newHarness()
	h.factions.missing = factionID(5)
	if _, err := h.service.CreateLeague(actorContext(authn.Admin), validInput(8)); !errors.Is(err, battleservice.ErrBattleFactionMissing) {
		t.Fatalf("faction err = %v", err)
	}
	h = newHarness()
	h.repo.createErr = errDatabase
	if _, err := h.service.CreateLeague(actorContext(authn.Admin), validInput(8)); !errors.Is(err, errDatabase) {
		t.Fatalf("storage err = %v", err)
	}
}

func TestCreateLeagueRetriesReturnTheStoredLeagueOnlyWhenIdentical(t *testing.T) {
	h := newHarness()
	admin := actorContext(authn.Admin)
	first, err := h.service.CreateLeague(admin, validInput(8))
	if err != nil {
		t.Fatal(err)
	}
	again, err := h.service.CreateLeague(admin, validInput(8))
	if err != nil || again.ID != first.ID || len(h.repo.leagues) != 1 {
		t.Fatalf("retry = %+v, %v", again, err)
	}
	changed := validInput(8)
	changed.TickCount = 400
	if _, err := h.service.CreateLeague(admin, changed); !errors.Is(err, ErrLeagueAlreadyExists) {
		t.Fatalf("changed retry err = %v", err)
	}
	other := authn.WithActor(context.Background(), authn.Actor{ID: "other-admin", Role: authn.Admin})
	if _, err := h.service.CreateLeague(other, validInput(8)); !errors.Is(err, ErrLeagueAlreadyExists) {
		t.Fatalf("other owner err = %v", err)
	}
}

func TestReadsRequireInspection(t *testing.T) {
	h := newHarness()
	if _, err := h.service.CreateLeague(actorContext(authn.Admin), validInput(8)); err != nil {
		t.Fatal(err)
	}
	developer := actorContext(authn.Developer)
	if got, err := h.service.GetLeague(developer, strings.ToUpper(leagueID)); err != nil || got.ID != leagueID {
		t.Fatalf("get = %+v, %v", got, err)
	}
	if list, err := h.service.ListLeagues(developer, 0); err != nil || len(list) != 1 || h.repo.listLimit != defaultListMax {
		t.Fatalf("list = %+v, %v, limit %d", list, err, h.repo.listLimit)
	}
	for _, limit := range []int{-1, 101} {
		if _, err := h.service.ListLeagues(developer, limit); !errors.Is(err, ErrInvalidLeague) {
			t.Errorf("limit %d: err = %v", limit, err)
		}
	}
	player := actorContext(authn.Player)
	if _, err := h.service.GetLeague(player, leagueID); !errors.Is(err, authn.ErrForbidden) {
		t.Errorf("player get err = %v", err)
	}
	if _, err := h.service.ListLeagues(player, 0); !errors.Is(err, authn.ErrForbidden) {
		t.Errorf("player list err = %v", err)
	}
	if _, err := h.service.GetLeague(developer, "league"); !errors.Is(err, ErrInvalidLeague) {
		t.Errorf("bad ID err = %v", err)
	}
	if _, err := h.service.GetLeague(developer, factionID(1)); !errors.Is(err, ErrLeagueNotFound) {
		t.Errorf("missing err = %v", err)
	}
}

func TestCancelLeagueStopsRunningBattlesAndRetriesFailedStops(t *testing.T) {
	h := newHarness()
	admin := actorContext(authn.Admin)
	if _, err := h.service.CreateLeague(admin, validInput(8)); err != nil {
		t.Fatal(err)
	}
	stored := h.repo.leagues[leagueID]
	battleID, other, finished := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333"
	stored.Fixtures[0].Status, stored.Fixtures[0].BattleID = league.FixtureRunning, &battleID
	stored.Fixtures[1].Status, stored.Fixtures[1].BattleID = league.FixtureRunning, &other
	stored.Fixtures[2].Status, stored.Fixtures[2].BattleID = league.FixtureCompleted, &finished
	h.repo.battles[battleID], h.repo.battles[other], h.repo.battles[finished] = "running", "queued", league.BattleCompleted
	h.battles.stopErrs = map[string]error{other: errDatabase}
	cancelled, err := h.service.CancelLeague(admin, leagueID)
	if err != nil || cancelled.Status != league.StatusCancelled || fmt.Sprint(h.battles.stopped) != fmt.Sprint([]string{battleID, other}) {
		t.Fatalf("cancel = %+v, %v, stopped %v", cancelled, err, h.battles.stopped)
	}
	h.battles.stopErrs = map[string]error{other: battleservice.ErrBattleNotActive}
	if again, err := h.service.CancelLeague(admin, leagueID); err != nil || again.Status != league.StatusCancelled || fmt.Sprint(h.battles.stopped) != fmt.Sprint([]string{battleID, other, other}) {
		t.Fatalf("repeat cancel = %+v, %v, stopped %v", again, err, h.battles.stopped)
	}
	if _, err := h.service.CancelLeague(actorContext(authn.Developer), leagueID); !errors.Is(err, authn.ErrForbidden) {
		t.Fatalf("developer cancel err = %v", err)
	}
	if _, err := h.service.CancelLeague(admin, "league"); !errors.Is(err, ErrInvalidLeague) {
		t.Fatalf("bad ID err = %v", err)
	}
	h.repo.leagues[leagueID].Status = league.StatusCompleted
	if _, err := h.service.CancelLeague(admin, leagueID); !errors.Is(err, ErrLeagueNotActive) {
		t.Fatalf("completed cancel err = %v", err)
	}
}

// memoryRepo implements Repository and SchedulerStore in memory.
type memoryRepo struct {
	leagues   map[string]*league.League
	battles   map[string]string
	origins   map[string]BattleProvenance
	cliSides  map[string]int
	cliSlots  int
	capErr    error
	lockHeld  bool
	createErr error
	storeErr  error
	listLimit int
	completed map[string]int
}

func copyLeague(l *league.League) *league.League {
	c := *l
	c.Entrants = append([]league.Entrant(nil), l.Entrants...)
	c.Fixtures = append([]league.Fixture(nil), l.Fixtures...)
	return &c
}

func (r *memoryRepo) CreateLeague(_ context.Context, l league.League) error {
	if r.createErr != nil {
		return r.createErr
	}
	if _, exists := r.leagues[l.ID]; exists {
		return ErrLeagueAlreadyExists
	}
	r.leagues[l.ID] = copyLeague(&l)
	return nil
}

func (r *memoryRepo) GetLeague(_ context.Context, id string) (*league.League, error) {
	l, ok := r.leagues[id]
	if !ok {
		return nil, ErrLeagueNotFound
	}
	return copyLeague(l), nil
}

func (r *memoryRepo) ListLeagues(_ context.Context, limit int) ([]league.Summary, error) {
	r.listLimit = limit
	summaries := []league.Summary{}
	for _, l := range r.leagues {
		summaries = append(summaries, league.Summary{ID: l.ID, Name: l.Name, Status: l.Status, GroupCount: l.GroupCount})
	}
	return summaries, nil
}

func (r *memoryRepo) CancelLeague(_ context.Context, id string) (bool, []string, error) {
	l, ok := r.leagues[id]
	if !ok {
		return false, nil, ErrLeagueNotFound
	}
	if l.Status == league.StatusCompleted {
		return false, nil, ErrLeagueNotActive
	}
	cancelled := l.Status == league.StatusRunning
	l.Status = league.StatusCancelled
	for i, f := range l.Fixtures {
		if !f.Settled() {
			l.Fixtures[i].Status = league.FixtureCancelled
		}
	}
	return cancelled, r.activeBattles(l), nil
}

func (r *memoryRepo) activeBattles(l *league.League) []string {
	active := []string{}
	for _, f := range l.Fixtures {
		if f.BattleID != nil && (r.battles[*f.BattleID] == "queued" || r.battles[*f.BattleID] == "running") {
			active = append(active, *f.BattleID)
		}
	}
	return active
}

func (r *memoryRepo) Schedule(ctx context.Context, fn func(context.Context, SchedulerStore) error) (bool, error) {
	if r.lockHeld {
		return false, nil
	}
	return true, fn(ctx, r)
}

func (r *memoryRepo) Exclusive(ctx context.Context, fn func(context.Context, SchedulerStore) error) error {
	return fn(ctx, r)
}

func (r *memoryRepo) League(_ context.Context, id string) (*league.League, error) {
	if r.storeErr != nil {
		return nil, r.storeErr
	}
	l, ok := r.leagues[id]
	if !ok {
		return nil, ErrLeagueNotFound
	}
	return r.withBattles(copyLeague(l)), nil
}

func (r *memoryRepo) Disqualify(_ context.Context, id string, entrant int, d league.Disqualification) error {
	d.At = time.Now()
	r.leagues[id].Entrants[entrant].Disqualified = &d
	return nil
}

func (r *memoryRepo) RunningLeagues(context.Context) ([]league.League, error) {
	if r.storeErr != nil {
		return nil, r.storeErr
	}
	running := []league.League{}
	for _, l := range r.leagues {
		if l.Status == league.StatusRunning {
			running = append(running, *r.withBattles(copyLeague(l)))
		}
	}
	return running, nil
}

// withBattles loads each fixture's battle status, as the PostgreSQL join does, unless a
// test set the outcome itself.
func (r *memoryRepo) withBattles(l *league.League) *league.League {
	for i, f := range l.Fixtures {
		if status, ok := r.battles[deref(f.BattleID)]; ok && f.Battle == nil {
			l.Fixtures[i].Battle = &league.BattleOutcome{Status: status}
		}
	}
	return l
}

func deref(id *string) string {
	if id == nil {
		return ""
	}
	return *id
}

func (r *memoryRepo) SaveFixture(_ context.Context, f league.Fixture) error {
	for _, l := range r.leagues {
		for i := range l.Fixtures {
			if l.Fixtures[i].ID == f.ID {
				l.Fixtures[i] = f
				return nil
			}
		}
	}
	return fmt.Errorf("fixture %q not found", f.ID)
}

func (r *memoryRepo) AddFixtures(_ context.Context, id string, fixtures []league.Fixture) error {
	r.leagues[id].Fixtures = append(r.leagues[id].Fixtures, fixtures...)
	return nil
}

func (r *memoryRepo) CompleteLeague(_ context.Context, id string, champion int) error {
	r.leagues[id].Status, r.leagues[id].Champion = league.StatusCompleted, &champion
	return nil
}

// Capacity counts queued and running battles and the CLI sides recorded for them.
func (r *memoryRepo) Capacity(context.Context) (Capacity, error) {
	capacity := Capacity{CLISlots: r.cliSlots}
	for id, status := range r.battles {
		if status == "queued" || status == "running" {
			capacity.Unfinished++
			capacity.CLISides += r.cliSides[id]
		}
	}
	return capacity, r.capErr
}

func (r *memoryRepo) CLISlots(context.Context) (int, error) { return r.cliSlots, r.capErr }

func (r *memoryRepo) Battle(_ context.Context, id string) (*BattleProvenance, error) {
	if _, ok := r.battles[id]; !ok {
		return nil, nil
	}
	origin := r.origins[id]
	return &origin, nil
}

// StrandedBattles lists active battles of cancelled leagues and forfeited fixtures.
func (r *memoryRepo) StrandedBattles(context.Context) ([]StrandedBattle, error) {
	stranded := []StrandedBattle{}
	for _, l := range r.leagues {
		for _, f := range l.Fixtures {
			status := r.battles[deref(f.BattleID)]
			if (l.Status == league.StatusCancelled || f.Status == league.FixtureForfeited) && (status == "queued" || status == "running") {
				stranded = append(stranded, StrandedBattle{LeagueID: l.ID, BattleID: *f.BattleID, OwnerID: l.OwnerID})
			}
		}
	}
	return stranded, nil
}

type fakeBattles struct {
	repo        *memoryRepo
	profiles    Profiles
	validateErr error
	createErr   error
	validated   []battleservice.CreateBattleInput
	created     []battleservice.CreateBattleInput
	creators    []authn.Actor
	stopped     []string
	stoppers    []authn.Actor
	stopErrs    map[string]error
}

func (b *fakeBattles) ValidateBattle(_ context.Context, input battleservice.CreateBattleInput) error {
	b.validated = append(b.validated, input)
	return b.validateErr
}

func (b *fakeBattles) CreateBattle(ctx context.Context, input battleservice.CreateBattleInput) (*battleservice.BattleRun, error) {
	if b.createErr != nil {
		return nil, b.createErr
	}
	actor, _ := authn.ActorFromContext(ctx)
	b.created, b.creators = append(b.created, input), append(b.creators, actor)
	b.repo.battles[input.ID] = "queued"
	for _, side := range input.Participants {
		if profile, ok := b.lookup(side.Controller.ProfileID); ok && side.Controller.Type == ai.ControllerModel && profile.Runtime == "cli" {
			b.repo.cliSides[input.ID]++
		}
	}
	b.repo.origins[input.ID] = BattleProvenance{OwnerID: actor.ID, DecisionBudgetExempt: input.DecisionBudgetExempt, ScenarioID: input.ScenarioID,
		FactionIDs: []string{input.Participants[0].FactionID, input.Participants[1].FactionID}}
	return &battleservice.BattleRun{ID: input.ID, Status: "queued"}, nil
}

func (b *fakeBattles) lookup(id string) (ai.ModelProfile, bool) {
	if b.profiles == nil {
		return ai.ModelProfile{}, false
	}
	return b.profiles.Get(id)
}

// StopBattle stops the battle unless an error is configured for it.
func (b *fakeBattles) StopBattle(ctx context.Context, id string) (*battleservice.BattleRun, error) {
	actor, _ := authn.ActorFromContext(ctx)
	b.stopped, b.stoppers = append(b.stopped, id), append(b.stoppers, actor)
	if err := b.stopErrs[id]; err != nil {
		return nil, err
	}
	b.repo.battles[id] = "stopped"
	return nil, nil
}

type fakeFactions struct{ missing string }

func (f *fakeFactions) GetFaction(_ context.Context, id string) (*faction.Faction, error) {
	if id == f.missing {
		return nil, factionservice.ErrFactionNotFound
	}
	var index int
	_, _ = fmt.Sscanf(id[len(id)-12:], "%d", &index)
	return &faction.Faction{ID: id, Name: fmt.Sprintf("Faction %d", index)}, nil
}
