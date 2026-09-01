package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	factionservice "github.com/twh09/autonomous_battlefield/backend/internal/faction/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
)

var (
	ErrBattleNotFound          = errors.New("battle not found")
	ErrBattleAlreadyExists     = errors.New("battle already exists")
	ErrBattleNotActive         = errors.New("battle is not active")
	ErrInvalidBattle           = errors.New("invalid battle")
	ErrBattleFactionMissing    = errors.New("battle faction not found")
	ErrScenarioNotFound        = errors.New("scenario not found")
	ErrIncompatibleFaction     = errors.New("faction is incompatible with scenario")
	ErrInvalidPerspective      = errors.New("invalid faction perspective")
	ErrModelProfileUnavailable = errors.New("model profile unavailable")
)

var battleUUIDPattern = regexp.MustCompile(`^[[:xdigit:]]{8}-[[:xdigit:]]{4}-[[:xdigit:]]{4}-[[:xdigit:]]{4}-[[:xdigit:]]{12}$`)

type CreateBattleInput struct {
	ID           string
	ScenarioID   string
	Participants []ParticipantInput
	TickCount    uint64
}

type ParticipantInput struct {
	FactionID  string
	Controller game.ControllerConfig
}

type CreateBattleParams struct {
	ID               string
	ScenarioID       string
	ScenarioDigest   string
	ScenarioSnapshot game.Scenario
	InitialState     game.State
	RequestedTicks   uint64
}

type BattleRun struct {
	ID               string        `json:"id"`
	ScenarioID       string        `json:"scenarioId"`
	ScenarioDigest   string        `json:"scenarioDigest"`
	ScenarioSnapshot game.Scenario `json:"scenario"`
	InitialState     game.State    `json:"initialState"`
	CurrentState     game.State    `json:"currentState"`
	RequestedTicks   uint64        `json:"requestedTicks"`
	CurrentTick      uint64        `json:"currentTick"`
	Status           string        `json:"status"`
	Paused           bool          `json:"paused"`
	Attempt          int           `json:"attempt"`
	Result           *game.Result  `json:"result,omitempty"`
	ErrorCode        *string       `json:"errorCode,omitempty"`
	ErrorMessage     *string       `json:"errorMessage,omitempty"`
	CreatedAt        time.Time     `json:"createdAt"`
	StartedAt        *time.Time    `json:"startedAt,omitempty"`
	CompletedAt      *time.Time    `json:"completedAt,omitempty"`
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
	StopBattle(context.Context, string) (*BattleRun, error)
	PauseBattle(context.Context, string) (*BattleRun, error)
	ResumeBattle(context.Context, string) (*BattleRun, error)
	ListBattleEvents(context.Context, string, *int, uint64, int, bool) (BattleEventPage, error)
}

type ModelProfileService interface {
	ListModelProfiles() []ai.ModelProfile
}

type DecisionHistory struct {
	RequestID       string     `json:"requestId"`
	FactionID       string     `json:"factionId"`
	TargetTick      uint64     `json:"targetTick"`
	Status          string     `json:"status"`
	ControllerType  string     `json:"controllerType"`
	ProfileID       string     `json:"profileId,omitempty"`
	Provider        string     `json:"provider,omitempty"`
	Runtime         string     `json:"runtime,omitempty"`
	ModelIdentifier string     `json:"modelIdentifier,omitempty"`
	FallbackSource  string     `json:"fallbackSource,omitempty"`
	ErrorCategory   string     `json:"errorCategory,omitempty"`
	ResolvedPlan    any        `json:"resolvedPlan,omitempty"`
	Usage           ai.Usage   `json:"usage"`
	CreatedAt       time.Time  `json:"createdAt"`
	ResolvedAt      *time.Time `json:"resolvedAt,omitempty"`
}

type DecisionHistoryService interface {
	ListDecisionHistory(context.Context, string, int) ([]DecisionHistory, error)
}

type decisionHistoryRepository interface {
	ListBattleDecisions(context.Context, string, int) ([]ai.DecisionRecord, error)
}

type PerspectiveService interface {
	GetFactionView(context.Context, string, string) (game.View, error)
}

func (s *battleService) ListScenarios() []ScenarioSummary {
	return s.catalog.Summaries()
}

type battleService struct {
	battles  Repository
	factions factionRepository
	catalog  ScenarioCatalog
	maxTicks uint64
	profiles *ai.ProfileRegistry
}

func NewService(battles Repository, factions factionRepository, catalog ScenarioCatalog, maxTicks uint64, registries ...*ai.ProfileRegistry) Service {
	registry := ai.NewProfileRegistry("local", nil)
	if len(registries) > 0 && registries[0] != nil {
		registry = registries[0]
	}
	return &battleService{battles: battles, factions: factions, catalog: catalog, maxTicks: maxTicks, profiles: registry}
}

func (s *battleService) ListModelProfiles() []ai.ModelProfile { return s.profiles.List() }

func (s *battleService) ListDecisionHistory(ctx context.Context, battleID string, attempt int) ([]DecisionHistory, error) {
	if !battleUUIDPattern.MatchString(battleID) || attempt < 1 {
		return nil, ErrInvalidBattle
	}
	repository, ok := s.battles.(decisionHistoryRepository)
	if !ok {
		return []DecisionHistory{}, nil
	}
	records, err := repository.ListBattleDecisions(ctx, battleID, attempt)
	if err != nil {
		return nil, err
	}
	result := make([]DecisionHistory, 0, len(records))
	for _, record := range records {
		result = append(result, DecisionHistory{RequestID: record.ID, FactionID: record.FactionID, TargetTick: record.TargetTick, Status: record.Status, ControllerType: record.ControllerType, ProfileID: record.ProfileID, Provider: record.Provider, Runtime: record.Runtime, ModelIdentifier: record.ModelIdentifier, FallbackSource: record.FallbackSource, ErrorCategory: record.ErrorCategory, ResolvedPlan: record.ResolvedPlan, Usage: record.Usage, CreatedAt: record.CreatedAt, ResolvedAt: record.ResolvedAt})
	}
	return result, nil
}

func (s *battleService) CreateBattle(ctx context.Context, input CreateBattleInput) (*BattleRun, error) {
	participants := input.Participants
	if !battleUUIDPattern.MatchString(input.ID) || strings.TrimSpace(input.ScenarioID) == "" ||
		len(participants) != 2 || participants[0].FactionID == participants[1].FactionID ||
		!battleUUIDPattern.MatchString(participants[0].FactionID) || !battleUUIDPattern.MatchString(participants[1].FactionID) ||
		input.TickCount == 0 || input.TickCount > s.maxTicks {
		return nil, ErrInvalidBattle
	}

	var identities [2]FactionIdentity
	controllers := make([]game.ControllerConfig, 2)
	for index, participant := range participants {
		factionID := participant.FactionID
		selected := participant.Controller
		switch selected.Type {
		case ai.ControllerScripted:
			if selected.ProfileID != "" {
				return nil, ErrInvalidBattle
			}
			selected = game.ControllerConfig{Type: ai.ControllerScripted}
		case ai.ControllerModel:
			profile, err := s.profiles.Require(selected.ProfileID)
			if err != nil {
				return nil, fmt.Errorf("%w: %q", ErrModelProfileUnavailable, selected.ProfileID)
			}
			selected = game.ControllerConfig{Type: ai.ControllerModel, ProfileID: profile.ID, Provider: profile.Provider, Runtime: profile.Runtime, ModelIdentifier: profile.ModelLabel}
		default:
			return nil, ErrInvalidBattle
		}
		controllers[index] = selected
		storedFaction, err := s.factions.GetFaction(ctx, factionID)
		if errors.Is(err, factionservice.ErrFactionNotFound) {
			return nil, fmt.Errorf("%w: %q", ErrBattleFactionMissing, factionID)
		}
		if err != nil {
			return nil, err
		}
		identities[index] = FactionIdentity{ID: storedFaction.ID, Name: storedFaction.Name, RulesetID: storedFaction.RulesetID, Blueprint: game.CloneBlueprint(storedFaction.Blueprint)}
	}

	resolved, digest, err := s.catalog.Resolve(input.ScenarioID, identities)
	if err != nil {
		return nil, err
	}
	if resolved.MaximumTicks > 0 && input.TickCount > resolved.MaximumTicks {
		return nil, ErrInvalidBattle
	}
	for index := range resolved.Factions {
		resolved.Factions[index].Controller = controllers[index]
	}
	payload, err := json.Marshal(resolved)
	if err != nil {
		return nil, fmt.Errorf("marshal resolved scenario: %w", err)
	}
	hash := sha256.Sum256(payload)
	digest = hex.EncodeToString(hash[:])
	match, err := game.NewMatch(input.ID, resolved, input.TickCount)
	if err != nil {
		return nil, fmt.Errorf("build initial battle state: %w", err)
	}
	initialState := match.State()
	err = s.battles.CreateBattle(ctx, CreateBattleParams{
		ID:               input.ID,
		ScenarioID:       resolved.ID,
		ScenarioDigest:   digest,
		ScenarioSnapshot: resolved,
		InitialState:     initialState,
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
		InitialState:     initialState, CurrentState: initialState,
		RequestedTicks: input.TickCount,
		Status:         "queued",
	}, nil
}

func (s *battleService) GetFactionView(ctx context.Context, battleID, factionID string) (game.View, error) {
	run, err := s.GetBattle(ctx, battleID)
	if err != nil {
		return game.View{}, err
	}
	view, err := game.ProjectView(run.ScenarioSnapshot.Map, run.CurrentState, factionID)
	if err != nil {
		return game.View{}, fmt.Errorf("%w: %v", ErrInvalidPerspective, err)
	}
	return view, nil
}

func (s *battleService) GetBattle(ctx context.Context, id string) (*BattleRun, error) {
	if !battleUUIDPattern.MatchString(id) {
		return nil, ErrInvalidBattle
	}
	run, err := s.battles.GetBattle(ctx, id)
	return run, err
}

func (s *battleService) StopBattle(ctx context.Context, id string) (*BattleRun, error) {
	if !battleUUIDPattern.MatchString(id) {
		return nil, ErrInvalidBattle
	}
	if err := s.battles.StopBattle(ctx, id); err != nil {
		return nil, err
	}
	return s.battles.GetBattle(ctx, id)
}

func (s *battleService) PauseBattle(ctx context.Context, id string) (*BattleRun, error) {
	return s.setBattlePaused(ctx, id, true)
}

func (s *battleService) ResumeBattle(ctx context.Context, id string) (*BattleRun, error) {
	return s.setBattlePaused(ctx, id, false)
}

func (s *battleService) setBattlePaused(ctx context.Context, id string, paused bool) (*BattleRun, error) {
	if !battleUUIDPattern.MatchString(id) {
		return nil, ErrInvalidBattle
	}
	if err := s.battles.SetBattlePaused(ctx, id, paused); err != nil {
		return nil, err
	}
	return s.battles.GetBattle(ctx, id)
}

func (s *battleService) ListBattleEvents(ctx context.Context, id string, requestedAttempt *int, after uint64, limit int, latest bool) (BattleEventPage, error) {
	if limit < 1 || limit > 500 || (latest && after != 0) {
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
	events, err := s.battles.ListBattleEvents(ctx, id, attempt, after, limit, latest)
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
