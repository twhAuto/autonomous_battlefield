// Validates and creates leagues, reports them, and cancels them. Admins create and
// cancel; developers and admins inspect. The scheduler that plays fixtures is in
// scheduler.go.

package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/league"
)

var (
	ErrInvalidLeague       = errors.New("invalid league")
	ErrLeagueNotFound      = errors.New("league not found")
	ErrLeagueAlreadyExists = errors.New("league already exists")
	ErrLeagueNotActive     = errors.New("league is not active")
)

const (
	maxNameLength  = 80
	maxScenarios   = 5
	defaultListMax = 25
	maxListLimit   = 100
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
	repo     Repository
	battles  Battles
	factions Factions
}

func New(repo Repository, battles Battles, factions Factions) *Service {
	return &Service{repo: repo, battles: battles, factions: factions}
}

// CreateLeague validates every entrant against every chosen map, then stores the league
// with its group schedule. Repeating the same request returns the stored league.
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
			err := s.battles.ValidateBattle(ctx, battleInput(validationBattleID, scenarioID, input.TickCount, input.TickDuration, input.DecisionIntervalTicks, home.FactionID, home.Controller, away.FactionID, away.Controller))
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
	created := &league.League{
		ID: input.ID, OwnerID: authn.Owner(ctx), Name: input.Name, Status: league.StatusRunning, RequestDigest: digest, GroupCount: groupCount,
		Settings: league.Settings{ScenarioIDs: append([]string(nil), input.ScenarioIDs...), TickCount: input.TickCount, TickDuration: input.TickDuration, DecisionIntervalTicks: input.DecisionIntervalTicks},
	}
	for index, entrant := range input.Entrants {
		stored, err := s.factions.GetFaction(ctx, entrant.FactionID)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", battleservice.ErrBattleFactionMissing, err)
		}
		created.Entrants = append(created.Entrants, league.Entrant{Index: index, Group: entrant.Group, FactionID: stored.ID, FactionName: stored.Name, Controller: entrant.Controller})
	}
	created.Fixtures = league.GroupFixtures(created.ID, created.Entrants, groupCount, created.Settings.ScenarioIDs)
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
	if err := requireInspect(ctx); err != nil {
		return nil, err
	}
	if !uuidPattern.MatchString(id) {
		return nil, ErrInvalidLeague
	}
	return s.repo.GetLeague(ctx, strings.ToLower(id))
}

// ListLeagues returns the newest leagues first. A limit of zero selects the default.
func (s *Service) ListLeagues(ctx context.Context, limit int) ([]league.Summary, error) {
	if err := requireInspect(ctx); err != nil {
		return nil, err
	}
	if limit == 0 {
		limit = defaultListMax
	}
	if limit < 0 || limit > maxListLimit {
		return nil, ErrInvalidLeague
	}
	return s.repo.ListLeagues(ctx, limit)
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
	cancelled, running, err := s.repo.CancelLeague(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, battleID := range running {
		if _, err := s.battles.StopBattle(ctx, battleID); err != nil && !errors.Is(err, battleservice.ErrBattleNotActive) {
			slog.WarnContext(ctx, "league_battle_stop_failed", "league_id", id, "battle_id", battleID, "error", err)
		}
	}
	if cancelled {
		slog.InfoContext(ctx, "league_cancelled", "league_id", id, "stopped_battles", len(running))
	}
	return s.repo.GetLeague(ctx, id)
}

func requireAdmin(ctx context.Context) error {
	if actor, ok := authn.ActorFromContext(ctx); !ok || actor.Role != authn.Admin {
		return authn.ErrForbidden
	}
	return nil
}

func requireInspect(ctx context.Context) error {
	if actor, ok := authn.ActorFromContext(ctx); !ok || !actor.CanInspect() {
		return authn.ErrForbidden
	}
	return nil
}

func battleInput(id, scenarioID string, ticks uint64, tickDuration *string, interval *uint64, homeFaction string, home game.ControllerConfig, awayFaction string, away game.ControllerConfig) battleservice.CreateBattleInput {
	return battleservice.CreateBattleInput{
		ID: id, ScenarioID: scenarioID, TickCount: ticks, TickDuration: tickDuration, DecisionIntervalTicks: interval,
		Participants: []battleservice.ParticipantInput{{FactionID: homeFaction, Controller: home}, {FactionID: awayFaction, Controller: away}},
	}
}
