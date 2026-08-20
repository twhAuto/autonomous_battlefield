package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	battle "github.com/twh09/autonomous_battlefield/backend/internal/battle/simulation"
	factionservice "github.com/twh09/autonomous_battlefield/backend/internal/faction/service"
)

var (
	ErrBattleNotFound       = errors.New("battle not found")
	ErrBattleAlreadyExists  = errors.New("battle already exists")
	ErrInvalidBattle        = errors.New("invalid battle")
	ErrBattleFactionMissing = errors.New("battle faction not found")
	ErrScenarioNotFound     = errors.New("scenario not found")
)

var battleUUIDPattern = regexp.MustCompile(`^[[:xdigit:]]{8}-[[:xdigit:]]{4}-[[:xdigit:]]{4}-[[:xdigit:]]{4}-[[:xdigit:]]{12}$`)

type CreateBattleInput struct {
	ID         string
	ScenarioID string
	FactionIDs []string
	TickCount  uint64
}

type CreateBattleParams struct {
	ID               string
	ScenarioID       string
	ScenarioDigest   string
	ScenarioSnapshot battle.Scenario
	RequestedTicks   uint64
}

type BattleRun struct {
	ID               string          `json:"id"`
	ScenarioID       string          `json:"scenarioId"`
	ScenarioDigest   string          `json:"scenarioDigest"`
	ScenarioSnapshot battle.Scenario `json:"scenario"`
	RequestedTicks   uint64          `json:"requestedTicks"`
	CurrentTick      uint64          `json:"currentTick"`
	Status           string          `json:"status"`
	Attempt          int             `json:"attempt"`
	Result           *battle.Result  `json:"result,omitempty"`
	ErrorCode        *string         `json:"errorCode,omitempty"`
	ErrorMessage     *string         `json:"errorMessage,omitempty"`
	CreatedAt        time.Time       `json:"createdAt"`
	StartedAt        *time.Time      `json:"startedAt,omitempty"`
	CompletedAt      *time.Time      `json:"completedAt,omitempty"`
}

type StoredBattleEvent struct {
	Attempt   int            `json:"attempt"`
	Sequence  uint64         `json:"sequence"`
	Tick      uint64         `json:"tick"`
	Type      string         `json:"type"`
	Data      map[string]any `json:"data"`
	CreatedAt time.Time      `json:"createdAt"`
}

type BattleEventPage struct {
	Attempt    int                 `json:"attempt"`
	Events     []StoredBattleEvent `json:"events"`
	NextCursor *uint64             `json:"nextCursor"`
}

type Service interface {
	ListScenarios() []ScenarioSummary
	CreateBattle(context.Context, CreateBattleInput) (*BattleRun, error)
	GetBattle(context.Context, string) (*BattleRun, error)
	ListBattleEvents(context.Context, string, *int, uint64, int) (BattleEventPage, error)
}

func (s *battleService) ListScenarios() []ScenarioSummary {
	return s.catalog.Summaries()
}

type battleService struct {
	battles  Repository
	factions factionRepository
	catalog  ScenarioCatalog
	maxTicks uint64
}

func NewService(battles Repository, factions factionRepository, catalog ScenarioCatalog, maxTicks uint64) Service {
	return &battleService{battles: battles, factions: factions, catalog: catalog, maxTicks: maxTicks}
}

func (s *battleService) CreateBattle(ctx context.Context, input CreateBattleInput) (*BattleRun, error) {
	if !battleUUIDPattern.MatchString(input.ID) || strings.TrimSpace(input.ScenarioID) == "" ||
		len(input.FactionIDs) != 2 || input.FactionIDs[0] == input.FactionIDs[1] ||
		!battleUUIDPattern.MatchString(input.FactionIDs[0]) || !battleUUIDPattern.MatchString(input.FactionIDs[1]) ||
		input.TickCount == 0 || input.TickCount > s.maxTicks {
		return nil, ErrInvalidBattle
	}

	var identities [2]FactionIdentity
	for index, factionID := range input.FactionIDs {
		storedFaction, err := s.factions.GetFaction(ctx, factionID)
		if errors.Is(err, factionservice.ErrFactionNotFound) {
			return nil, fmt.Errorf("%w: %q", ErrBattleFactionMissing, factionID)
		}
		if err != nil {
			return nil, err
		}
		identities[index] = FactionIdentity{ID: storedFaction.ID, Name: storedFaction.Name}
	}

	resolved, digest, err := s.catalog.Resolve(input.ScenarioID, identities)
	if err != nil {
		return nil, err
	}
	err = s.battles.CreateBattle(ctx, CreateBattleParams{
		ID:               input.ID,
		ScenarioID:       resolved.ID,
		ScenarioDigest:   digest,
		ScenarioSnapshot: resolved,
		RequestedTicks:   input.TickCount,
	})
	if errors.Is(err, ErrBattleAlreadyExists) {
		return nil, ErrBattleAlreadyExists
	}
	if err != nil {
		return nil, err
	}
	return &BattleRun{
		ID:               input.ID,
		ScenarioID:       resolved.ID,
		ScenarioDigest:   digest,
		ScenarioSnapshot: resolved,
		RequestedTicks:   input.TickCount,
		Status:           "queued",
	}, nil
}

func (s *battleService) GetBattle(ctx context.Context, id string) (*BattleRun, error) {
	if !battleUUIDPattern.MatchString(id) {
		return nil, ErrInvalidBattle
	}
	run, err := s.battles.GetBattle(ctx, id)
	return run, err
}

func (s *battleService) ListBattleEvents(ctx context.Context, id string, requestedAttempt *int, after uint64, limit int) (BattleEventPage, error) {
	if limit < 1 || limit > 500 {
		return BattleEventPage{}, ErrInvalidBattle
	}
	run, err := s.GetBattle(ctx, id)
	if err != nil {
		return BattleEventPage{}, err
	}
	attempt := run.Attempt
	if requestedAttempt != nil {
		if *requestedAttempt < 0 || *requestedAttempt > run.Attempt {
			return BattleEventPage{}, ErrInvalidBattle
		}
		attempt = *requestedAttempt
	}
	events, err := s.battles.ListBattleEvents(ctx, id, attempt, after, limit)
	if err != nil {
		return BattleEventPage{}, err
	}
	page := BattleEventPage{Attempt: attempt, Events: events}
	if len(events) > 0 {
		cursor := events[len(events)-1].Sequence
		page.NextCursor = &cursor
	}
	return page, nil
}
