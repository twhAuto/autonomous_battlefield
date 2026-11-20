// Validates and creates leagues, reports them, cancels them and disqualifies entrants.
// Admins create, cancel and disqualify; every signed-in person reads them. The
// scheduler that plays fixtures is in scheduler.go.

package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/authn"
	battleservice "github.com/twhAuto/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/game"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/league"
)

var (
	ErrInvalidLeague       = errors.New("invalid league")
	ErrLeagueNotFound      = errors.New("league not found")
	ErrLeagueAlreadyExists = errors.New("league already exists")
	ErrLeagueNotActive     = errors.New("league is not active")
)

const (
	maxNameLength = 80
	// maxReasonLength bounds a disqualification's reason, in characters.
	maxReasonLength = 200
	maxScenarios    = 5
	defaultListMax  = 25
	maxListLimit    = 100
	// validationBattleID stands in for the real battle IDs while validating entrants.
	validationBattleID = "00000000-0000-4000-8000-000000000000"
)

var uuidPattern = regexp.MustCompile(`^[[:xdigit:]]{8}-[[:xdigit:]]{4}-[[:xdigit:]]{4}-[[:xdigit:]]{4}-[[:xdigit:]]{12}$`)

type CreateInput struct {
	ID                    string         `json:"id"`
	Name                  string         `json:"name"`
	ScenarioIDs           []string       `json:"scenarioIds"`
	TickCount             uint64         `json:"tickCount"`
	TickDuration          *string        `json:"tickDuration,omitempty"`
	DecisionIntervalTicks *uint64        `json:"decisionIntervalTicks,omitempty"`
	Entrants              []EntrantInput `json:"entrants"`
}

// EntrantInput places a faction and its controller in a group, 0 being group A.
type EntrantInput struct {
	FactionID  string                `json:"factionId"`
	Group      int                   `json:"group"`
	Controller game.ControllerConfig `json:"controller"`
}

type Service struct {
	repo        Repository
	battles     Battles
	factions    Factions
	profiles    Profiles
	battleSlots int
}

// Options sets how many league battles the scheduler may keep running.
type Options struct {
	// BattleSlots is the engine's battle capacity (engine.concurrency); zero means one.
	BattleSlots int
	// Profiles resolves which model controllers run as CLI processes.
	Profiles Profiles
}

func New(repo Repository, battles Battles, factions Factions, options Options) *Service {
	return &Service{repo: repo, battles: battles, factions: factions, profiles: options.Profiles, battleSlots: max(1, options.BattleSlots)}
}

// CreateLeague validates every entrant against every chosen map, then stores the league
// with its opening matches. Repeating the same request returns the stored league.
func (s *Service) CreateLeague(ctx context.Context, input CreateInput) (*league.League, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	input = normalized(input)
	groupCount, ok := validShape(input)
	if !ok {
		return nil, ErrInvalidLeague
	}
	if err := s.validateBattles(ctx, input); err != nil {
		return nil, err
	}
	created, err := s.newLeague(ctx, input, groupCount)
	if err != nil {
		return nil, err
	}
	err = s.repo.CreateLeague(ctx, *created)
	if errors.Is(err, ErrLeagueAlreadyExists) {
		existing, getErr := s.repo.GetLeague(ctx, input.ID)
		if getErr == nil && existing.OwnerID == created.OwnerID && existing.RequestDigest == created.RequestDigest {
			return existing, nil
		}
		return nil, ErrLeagueAlreadyExists
	}
	if err != nil {
		return nil, err
	}
	slog.InfoContext(ctx, "league_created", "league_id", created.ID, "entrants", len(created.Entrants), "fixtures", len(created.Fixtures))
	return s.repo.GetLeague(ctx, created.ID)
}

// normalized trims the name and lowercases IDs on a copy of the request.
func normalized(input CreateInput) CreateInput {
	input.Name = strings.TrimSpace(input.Name)
	input.ID = strings.ToLower(input.ID)
	input.Entrants = append([]EntrantInput(nil), input.Entrants...)
	for i := range input.Entrants {
		input.Entrants[i].FactionID = strings.ToLower(input.Entrants[i].FactionID)
	}
	return input
}

func validShape(input CreateInput) (int, bool) {
	entrants := len(input.Entrants)
	if !uuidPattern.MatchString(input.ID) || input.Name == "" || len([]rune(input.Name)) > maxNameLength || input.TickCount == 0 ||
		(entrants != 2*league.GroupSize && entrants != 4*league.GroupSize) || !validScenarios(input.ScenarioIDs) {
		return 0, false
	}
	groupCount := entrants / league.GroupSize
	sizes := make([]int, groupCount)
	factions := map[string]bool{}
	for _, entrant := range input.Entrants {
		if entrant.Group < 0 || entrant.Group >= groupCount || !uuidPattern.MatchString(entrant.FactionID) || factions[entrant.FactionID] {
			return 0, false
		}
		factions[entrant.FactionID] = true
		sizes[entrant.Group]++
	}
	for _, size := range sizes {
		if size != league.GroupSize {
			return 0, false
		}
	}
	return groupCount, true
}

func validScenarios(ids []string) bool {
	seen := map[string]bool{}
	for _, id := range ids {
		if strings.TrimSpace(id) == "" || seen[id] {
			return false
		}
		seen[id] = true
	}
	return len(ids) > 0 && len(ids) <= maxScenarios
}

// validateBattles checks every entrant on every map, from both spawn sides, with the
// same checks battle creation applies: factions, model access and timing.
func (s *Service) validateBattles(ctx context.Context, input CreateInput) error {
	count := len(input.Entrants)
	for _, scenarioID := range input.ScenarioIDs {
		for i := range input.Entrants {
			home, away := input.Entrants[i], input.Entrants[(i+1)%count]
			settings := league.Settings{TickCount: input.TickCount, TickDuration: input.TickDuration, DecisionIntervalTicks: input.DecisionIntervalTicks}
			err := s.battles.ValidateBattle(ctx, battleInput(validationBattleID, scenarioID, settings,
				battleservice.ParticipantInput{FactionID: home.FactionID, Controller: home.Controller},
				battleservice.ParticipantInput{FactionID: away.FactionID, Controller: away.Controller}))
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) newLeague(ctx context.Context, input CreateInput, groupCount int) (*league.League, error) {
	digest, err := requestDigest(input)
	if err != nil {
		return nil, err
	}
	var salt [16]byte
	if _, err := rand.Read(salt[:]); err != nil {
		return nil, fmt.Errorf("generate league battle salt: %w", err)
	}
	created := &league.League{
		ID: input.ID, OwnerID: authn.Owner(ctx), Name: input.Name, Status: league.StatusRunning, RequestDigest: digest, BattleSalt: hex.EncodeToString(salt[:]), GroupCount: groupCount,
		Settings: league.Settings{ScenarioIDs: append([]string(nil), input.ScenarioIDs...), TickCount: input.TickCount, TickDuration: input.TickDuration, DecisionIntervalTicks: input.DecisionIntervalTicks},
	}
	for index, entrant := range input.Entrants {
		stored, err := s.factions.GetFaction(ctx, entrant.FactionID)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", battleservice.ErrBattleFactionMissing, err)
		}
		created.Entrants = append(created.Entrants, league.Entrant{Index: index, Group: entrant.Group, FactionID: stored.ID, FactionName: stored.Name, Controller: entrant.Controller})
	}
	created.Fixtures = created.OpeningFixtures()
	return created, nil
}

// requestDigest fingerprints a create request, so only an identical retry is a replay.
func requestDigest(input CreateInput) (string, error) {
	payload, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("marshal league request: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func (s *Service) GetLeague(ctx context.Context, id string) (*league.League, error) {
	if err := requireView(ctx); err != nil {
		return nil, err
	}
	if !uuidPattern.MatchString(id) {
		return nil, ErrInvalidLeague
	}
	return s.repo.GetLeague(ctx, strings.ToLower(id))
}

// ListQuery selects leagues to list: at most Limit, and with a non-empty RulesVersion
// only leagues with a battle on that rules version.
type ListQuery struct {
	Limit        int
	RulesVersion string
}

// ListLeagues returns the newest leagues first. A limit of zero selects the default.
func (s *Service) ListLeagues(ctx context.Context, query ListQuery) ([]league.Summary, error) {
	if err := requireView(ctx); err != nil {
		return nil, err
	}
	if query.Limit == 0 {
		query.Limit = defaultListMax
	}
	if query.Limit < 0 || query.Limit > maxListLimit || len(query.RulesVersion) > game.MaxRulesVersionLength {
		return nil, ErrInvalidLeague
	}
	return s.repo.ListLeagues(ctx, query)
}

// Scheduling reports the capacity league battles run in.
func (s *Service) Scheduling(ctx context.Context) (league.Scheduling, error) {
	if err := requireView(ctx); err != nil {
		return league.Scheduling{}, err
	}
	cliSlots, err := s.repo.CLISlots(ctx)
	return league.Scheduling{BattleSlots: s.battleSlots, CLISlots: cliSlots}, err
}

// CancelLeague stops a running league: unfinished fixtures are cancelled and running
// battles stopped. Cancelling a cancelled league returns it unchanged.
func (s *Service) CancelLeague(ctx context.Context, id string) (*league.League, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	if !uuidPattern.MatchString(id) {
		return nil, ErrInvalidLeague
	}
	id = strings.ToLower(id)
	cancelled, active, err := s.repo.CancelLeague(ctx, id)
	if err != nil {
		return nil, err
	}
	// A stop that fails here is retried by repeating the cancellation, and by every
	// scheduling pass, which stops battles left running in cancelled leagues.
	for _, battleID := range active {
		s.stopBattle(ctx, id, battleID)
	}
	if cancelled {
		slog.InfoContext(ctx, "league_cancelled", "league_id", id, "active_battles", len(active))
	}
	return s.repo.GetLeague(ctx, id)
}

// DisqualifyEntrant removes an entrant from a running league, with an optional reason.
// It forfeits the entrant's open matches and stops their battles, and every later match
// the entrant reaches opens forfeited. Disqualifying it again returns the league
// unchanged.
func (s *Service) DisqualifyEntrant(ctx context.Context, id string, entrant int, reason string) (*league.League, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	reason = strings.TrimSpace(reason)
	if !uuidPattern.MatchString(id) || len([]rune(reason)) > maxReasonLength {
		return nil, ErrInvalidLeague
	}
	id = strings.ToLower(id)
	d := league.Disqualification{By: authn.Owner(ctx), Reason: reason}
	var active []string
	err := s.repo.Exclusive(ctx, func(ctx context.Context, store SchedulerStore) error {
		var err error
		active, err = disqualify(ctx, store, id, entrant, d)
		return err
	})
	if err != nil {
		return nil, err
	}
	// A stop that fails here is retried by every scheduling pass.
	for _, battleID := range active {
		s.stopBattle(ctx, id, battleID)
	}
	return s.repo.GetLeague(ctx, id)
}

// disqualify records the disqualification and its forfeits, then opens the matches
// that can follow. It returns the forfeited battles still queued or running.
func disqualify(ctx context.Context, store SchedulerStore, id string, entrant int, d league.Disqualification) ([]string, error) {
	l, err := store.League(ctx, id)
	if err != nil {
		return nil, err
	}
	if l.Status != league.StatusRunning {
		return nil, ErrLeagueNotActive
	}
	forfeited, err := l.Disqualify(entrant, d)
	if errors.Is(err, league.ErrAlreadyDisqualified) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := store.Disqualify(ctx, id, entrant, d); err != nil {
		return nil, err
	}
	active, err := saveForfeits(ctx, store, l, forfeited)
	if err != nil {
		return nil, err
	}
	slog.InfoContext(ctx, "league_entrant_disqualified", "league_id", id, "entrant", entrant, "forfeited_fixtures", len(forfeited), "active_battles", len(active))
	return active, settle(ctx, store, l)
}

// saveForfeits stores forfeited fixtures and returns the battles to stop: those still
// queued or running, and any a pass created for a pending fixture before it rolled back.
func saveForfeits(ctx context.Context, store SchedulerStore, l *league.League, forfeited []league.Fixture) ([]string, error) {
	active := []string{}
	for _, f := range forfeited {
		f, err := linkInterruptedStart(ctx, store, l, f)
		if err != nil {
			return nil, err
		}
		if err := store.SaveFixture(ctx, f); err != nil {
			return nil, err
		}
		if f.BattleID != nil && (f.Battle == nil || f.Battle.Status == "queued" || f.Battle.Status == "running") {
			active = append(active, *f.BattleID)
		}
	}
	return active, nil
}

// linkInterruptedStart links a pending fixture to the battle a scheduling pass created
// for its next attempt before rolling back. The scheduler adopts such a battle only for
// a pending fixture, so a forfeit links it instead and the battle is stopped.
func linkInterruptedStart(ctx context.Context, store SchedulerStore, l *league.League, f league.Fixture) (league.Fixture, error) {
	if f.BattleID != nil {
		return f, nil
	}
	battleID := league.FixtureBattleID(l.BattleSalt, f.ID, f.Attempts+1)
	existing, err := store.Battle(ctx, battleID)
	if err != nil || existing == nil || !createdFor(l, f, *existing) {
		return f, err
	}
	f.Attempts++
	f.BattleID = &battleID
	return f, nil
}

func (s *Service) stopBattle(ctx context.Context, leagueID, battleID string) {
	if _, err := s.battles.StopBattle(ctx, battleID); err != nil && !errors.Is(err, battleservice.ErrBattleNotActive) {
		slog.WarnContext(ctx, "league_battle_stop_failed", "league_id", leagueID, "battle_id", battleID, "error", err)
	}
}

func requireAdmin(ctx context.Context) error {
	if actor, ok := authn.ActorFromContext(ctx); !ok || actor.Role != authn.Admin {
		return authn.ErrForbidden
	}
	return nil
}

func requireView(ctx context.Context) error {
	if actor, ok := authn.ActorFromContext(ctx); !ok || !actor.CanView() {
		return authn.ErrForbidden
	}
	return nil
}

// battleInput builds a league battle request with the home side first.
func battleInput(id, scenarioID string, settings league.Settings, home, away battleservice.ParticipantInput) battleservice.CreateBattleInput {
	return battleservice.CreateBattleInput{
		ID: id, ScenarioID: scenarioID, TickCount: settings.TickCount, TickDuration: settings.TickDuration, DecisionIntervalTicks: settings.DecisionIntervalTicks,
		Participants: []battleservice.ParticipantInput{home, away},
	}
}
