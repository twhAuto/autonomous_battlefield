// Validates battle requests, freezes scenario inputs, and coordinates battle reads,
// controls, perspectives, and optional result caching.

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

	"github.com/twh09/autonomous_battlefield/backend/internal/ai/profile"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	factionservice "github.com/twh09/autonomous_battlefield/backend/internal/faction/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/observability/tracing"
)

var (
	ErrLimited                 = errors.New("battle allowance exhausted")
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
	ID                    string
	ScenarioID            string
	Participants          []ParticipantInput
	TickCount             uint64
	TickDuration          *string
	DecisionIntervalTicks *uint64
	// DecisionBudgetExempt lifts the per-battle model decision budget. Only the league
	// scheduler sets it, and it applies only to an Admin actor; HTTP requests never do.
	DecisionBudgetExempt bool
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
	Traceparent      string
	Tracestate       string
	// AdmissionExempt skips the per-person and global battle allowances; only an
	// Admin actor sets it.
	AdmissionExempt bool
	// TesterAllowance applies runtime_policy's Tester allowances instead of the Player
	// ones; only a Tester actor sets it.
	TesterAllowance bool
	// DecisionBudgetExempt lets model decisions continue past runtime_policy's
	// per-battle budget; executions are still counted.
	DecisionBudgetExempt bool
}

type BattleRun struct {
	OwnerID          string        `json:"ownerId"`
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
	League           *BattleLeague `json:"league,omitempty"`
}

// BattleLeague places a league battle in its league; it never changes once set. Match
// names a group fixture's match; the HTTP API fills it in.
type BattleLeague struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	FixtureID string  `json:"fixtureId"`
	Stage     string  `json:"stage"`
	Group     *string `json:"group,omitempty"`
	Round     int     `json:"round"`
	Tie       int     `json:"tie"`
	Match     string  `json:"match,omitempty"`
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
	ListBattles(context.Context, BattleHistoryInput) (BattleHistoryPage, error)
	ListRulesVersions(context.Context) (RulesVersions, error)
	ListScenarios() []ScenarioSummary
	CreateBattle(context.Context, CreateBattleInput) (*BattleRun, error)
	// ValidateBattle runs every CreateBattle check without creating the battle.
	ValidateBattle(context.Context, CreateBattleInput) error
	GetBattle(context.Context, string) (*BattleRun, error)
	StopBattle(context.Context, string) (*BattleRun, error)
	PauseBattle(context.Context, string) (*BattleRun, error)
	ResumeBattle(context.Context, string) (*BattleRun, error)
	ListBattleEvents(context.Context, string, *int, uint64, int, bool) (BattleEventPage, error)
	AllowanceService
	ModelProfileService
	DecisionHistoryService
	PerspectiveService
	DecisionContextService
	ReplayService
}

type ModelProfileService interface {
	ListModelProfiles() []ai.ModelProfile
}

type DecisionHistory struct {
	ContextDelivery   string     `json:"contextDelivery,omitempty"`
	CLIProcessStartMS *int64     `json:"cliProcessStartMs,omitempty"`
	ReasoningEffort   string     `json:"reasoningEffort,omitempty"`
	ClaimedAt         *time.Time `json:"claimedAt,omitempty"`
	ContextServedAt   *time.Time `json:"contextServedAt,omitempty"`
	RunnerCompletedAt *time.Time `json:"runnerCompletedAt,omitempty"`
	TraceURL          string     `json:"traceUrl,omitempty"`
	RequestID         string     `json:"requestId"`
	FactionID         string     `json:"factionId"`
	TargetTick        uint64     `json:"targetTick"`
	Status            string     `json:"status"`
	ControllerType    string     `json:"controllerType"`
	ProfileID         string     `json:"profileId,omitempty"`
	Provider          string     `json:"provider,omitempty"`
	Runtime           string     `json:"runtime,omitempty"`
	ModelIdentifier   string     `json:"modelIdentifier,omitempty"`
	FallbackSource    string     `json:"fallbackSource,omitempty"`
	ErrorCategory     string     `json:"errorCategory,omitempty"`
	ResolvedPlan      any        `json:"resolvedPlan,omitempty"`
	Usage             ai.Usage   `json:"usage"`
	CreatedAt         time.Time  `json:"createdAt"`
	ResolvedAt        *time.Time `json:"resolvedAt,omitempty"`
}

type DecisionHistoryService interface {
	ListDecisionHistory(context.Context, string, int) ([]DecisionHistory, error)
}

type PerspectiveService interface {
	GetFactionView(context.Context, string, string) (game.View, error)
}

func (s *battleService) ListScenarios() []ScenarioSummary {
	return s.catalog.Summaries()
}

type battleService struct {
	battles     Repository
	factions    factionRepository
	catalog     ScenarioCatalog
	maxTicks    uint64
	profiles    *profile.Registry
	resultCache ResultCache
}

// Options configures optional battle service dependencies. A nil Profiles uses the
// local registry; a nil Cache disables caching of terminal battle snapshots.
type Options struct {
	Cache    ResultCache
	Profiles *profile.Registry
}

func NewService(battles Repository, factions factionRepository, catalog ScenarioCatalog, maxTicks uint64, options Options) Service {
	registry := options.Profiles
	if registry == nil {
		registry = profile.NewRegistry("local", nil)
	}
	return &battleService{battles: battles, factions: factions, catalog: catalog, maxTicks: maxTicks, profiles: registry, resultCache: options.Cache}
}

func (s *battleService) ListModelProfiles() []ai.ModelProfile { return s.profiles.List() }

func (s *battleService) ListDecisionHistory(ctx context.Context, battleID string, attempt int) ([]DecisionHistory, error) {
	if !battleUUIDPattern.MatchString(battleID) || attempt < 1 {
		return nil, ErrInvalidBattle
	}
	records, err := s.battles.ListBattleDecisions(ctx, battleID, attempt)
	if err != nil {
		return nil, err
	}
	result := make([]DecisionHistory, 0, len(records))
	for _, record := range records {
		traceURL := ""
		if id, ok := record.ProviderMetadata["aiTraceId"].(string); ok && id == tracing.DecisionTraceID(record.ID) {
			traceURL = tracing.DecisionTraceURL(record.ID)
		}
		result = append(result, DecisionHistory{ContextDelivery: ai.ContextDelivery(record.ProviderMetadata), CLIProcessStartMS: ai.MetadataMilliseconds(record.ProviderMetadata, "cliProcessStartMs"), ReasoningEffort: record.ReasoningEffort, ClaimedAt: record.ClaimedAt, ContextServedAt: record.ContextServedAt, RunnerCompletedAt: record.RunnerCompletedAt, TraceURL: traceURL, RequestID: record.ID, FactionID: record.FactionID, TargetTick: record.TargetTick, Status: record.Status, ControllerType: record.ControllerType, ProfileID: record.ProfileID, Provider: record.Provider, Runtime: record.Runtime, ModelIdentifier: record.ModelIdentifier, FallbackSource: record.FallbackSource, ErrorCategory: record.ErrorCategory, ResolvedPlan: record.ResolvedPlan, Usage: record.Usage, CreatedAt: record.CreatedAt, ResolvedAt: record.ResolvedAt})
	}
	return result, nil
}

func (s *battleService) CreateBattle(ctx context.Context, input CreateBattleInput) (*BattleRun, error) {
	prepared, err := s.prepareBattle(ctx, input)
	if err != nil {
		return nil, err
	}
	resolved, digest, initialState := prepared.scenario, prepared.digest, prepared.initialState
	traceparent, tracestate := tracing.TraceContext(ctx)
	err = s.battles.CreateBattle(ctx, CreateBattleParams{
		ID:                   input.ID,
		ScenarioID:           resolved.ID,
		ScenarioDigest:       digest,
		ScenarioSnapshot:     resolved,
		InitialState:         initialState,
		RequestedTicks:       input.TickCount,
		Traceparent:          traceparent,
		Tracestate:           tracestate,
		AdmissionExempt:      isAdmin(ctx),
		TesterAllowance:      hasRole(ctx, authn.Tester),
		DecisionBudgetExempt: input.DecisionBudgetExempt && isAdmin(ctx),
	})
	if errors.Is(err, ErrBattleAlreadyExists) {
		return s.replayCreate(ctx, input, digest)
	}
	if err != nil {
		return nil, err
	}
	return &BattleRun{
		OwnerID:          authn.Owner(ctx),
		ID:               input.ID,
		ScenarioID:       resolved.ID,
		ScenarioDigest:   digest,
		ScenarioSnapshot: resolved,
		InitialState:     initialState, CurrentState: initialState,
		RequestedTicks: input.TickCount,
		Status:         "queued",
	}, nil
}

func (s *battleService) ValidateBattle(ctx context.Context, input CreateBattleInput) error {
	_, err := s.prepareBattle(ctx, input)
	return err
}

type preparedBattle struct {
	scenario     game.Scenario
	digest       string
	initialState game.State
}

// prepareBattle validates a request and freezes its scenario, controllers and initial
// state without persisting anything.
func (s *battleService) prepareBattle(ctx context.Context, input CreateBattleInput) (preparedBattle, error) {
	if !validCreateBattleInput(input, s.maxTicks) {
		return preparedBattle{}, ErrInvalidBattle
	}
	identities, controllers, err := s.resolveParticipants(ctx, input.Participants)
	if err != nil {
		return preparedBattle{}, err
	}
	resolved, err := s.catalog.Resolve(input.ScenarioID, identities)
	if err != nil {
		return preparedBattle{}, err
	}
	if resolved.MaximumTicks > 0 && input.TickCount > resolved.MaximumTicks {
		return preparedBattle{}, ErrInvalidBattle
	}
	if err := applyBattleTiming(&resolved, input); err != nil {
		return preparedBattle{}, err
	}
	for index := range resolved.Factions {
		resolved.Factions[index].Controller = controllers[index]
	}
	digest, err := ScenarioDigest(resolved)
	if err != nil {
		return preparedBattle{}, err
	}
	match, err := game.NewMatch(input.ID, resolved, input.TickCount)
	if err != nil {
		return preparedBattle{}, fmt.Errorf("build initial battle state: %w", err)
	}
	return preparedBattle{scenario: resolved, digest: digest, initialState: match.State()}, nil
}

// ScenarioDigest fingerprints a battle's frozen scenario, including its controllers. A
// create retry is a replay only when the digest matches the stored battle.
func ScenarioDigest(scenario game.Scenario) (string, error) {
	payload, err := json.Marshal(scenario)
	if err != nil {
		return "", fmt.Errorf("marshal resolved scenario: %w", err)
	}
	hash := sha256.Sum256(payload)
	return hex.EncodeToString(hash[:]), nil
}

// replayCreate returns the stored battle when a create retry repeats the same
// caller's request, and a conflict for any other reuse of the client ID.
func (s *battleService) replayCreate(ctx context.Context, input CreateBattleInput, digest string) (*BattleRun, error) {
	existing, err := s.battles.GetBattle(ctx, input.ID)
	if err != nil && !errors.Is(err, ErrBattleNotFound) {
		// An unverified retry must surface as a server error, not a conflict.
		return nil, err
	}
	if err == nil && existing != nil && existing.OwnerID == authn.Owner(ctx) && existing.ScenarioDigest == digest && existing.RequestedTicks == input.TickCount {
		return existing, nil
	}
	return nil, ErrBattleAlreadyExists
}

func validCreateBattleInput(input CreateBattleInput, maxTicks uint64) bool {
	participants := input.Participants
	return battleUUIDPattern.MatchString(input.ID) && strings.TrimSpace(input.ScenarioID) != "" &&
		len(participants) == 2 && participants[0].FactionID != participants[1].FactionID &&
		battleUUIDPattern.MatchString(participants[0].FactionID) && battleUUIDPattern.MatchString(participants[1].FactionID) &&
		input.TickCount > 0 && input.TickCount <= maxTicks
}

func (s *battleService) resolveParticipants(ctx context.Context, participants []ParticipantInput) ([2]FactionIdentity, [2]game.ControllerConfig, error) {
	var identities [2]FactionIdentity
	var controllers [2]game.ControllerConfig
	for index, participant := range participants {
		controller, err := s.resolveController(ctx, participant.Controller)
		if err != nil {
			return identities, controllers, err
		}
		storedFaction, err := s.factions.GetFaction(ctx, participant.FactionID)
		if errors.Is(err, factionservice.ErrFactionNotFound) {
			return identities, controllers, fmt.Errorf("%w: %q", ErrBattleFactionMissing, participant.FactionID)
		}
		if err != nil {
			return identities, controllers, err
		}
		controllers[index] = controller
		identities[index] = FactionIdentity{ID: storedFaction.ID, Name: storedFaction.Name, Doctrine: storedFaction.Doctrine, Background: storedFaction.Background, RulesetID: storedFaction.RulesetID, Blueprint: game.CloneBlueprint(storedFaction.Blueprint)}
	}
	return identities, controllers, nil
}

func (s *battleService) resolveController(ctx context.Context, selected game.ControllerConfig) (game.ControllerConfig, error) {
	switch selected.Type {
	case ai.ControllerScripted:
		if selected.ProfileID != "" || selected.ModelIdentifier != "" || selected.ReasoningEffort != "" {
			return game.ControllerConfig{}, ErrInvalidBattle
		}
		return game.ControllerConfig{Type: ai.ControllerScripted}, nil
	case ai.ControllerModel:
		return s.resolveModelController(ctx, selected)
	default:
		return game.ControllerConfig{}, ErrInvalidBattle
	}
}

// resolveModelController freezes a model selection. With an actor, the profile and the
// model must be ones the actor's role may use, and an omitted model selects the first
// the role may use when it may not use the configured one.
func (s *battleService) resolveModelController(ctx context.Context, selected game.ControllerConfig) (game.ControllerConfig, error) {
	p, err := s.profiles.Require(selected.ProfileID)
	if err != nil {
		return game.ControllerConfig{}, fmt.Errorf("%w: %q", ErrModelProfileUnavailable, selected.ProfileID)
	}
	if _, hasActor := authn.ActorFromContext(ctx); hasActor {
		selectable, ok := profile.ForRole(ctx, p)
		if !ok || (selected.ModelIdentifier != "" && !profile.ModelAllowed(ctx, p, selected.ModelIdentifier)) {
			return game.ControllerConfig{}, authn.ErrForbidden
		}
		p = selectable
	}
	model, effort, err := ai.ResolveModelSelection(p, selected.ModelIdentifier, selected.ReasoningEffort)
	if err != nil {
		return game.ControllerConfig{}, fmt.Errorf("%w: %s", ErrModelProfileUnavailable, err)
	}
	return game.ControllerConfig{Type: ai.ControllerModel, ProfileID: p.ID, Provider: p.Provider, Runtime: p.Runtime, ModelIdentifier: model, ReasoningEffort: effort}, nil
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
	id = strings.ToLower(id)
	if s.resultCache != nil {
		if run, err := s.resultCache.Get(ctx, id); err == nil && cacheableResult(run, id) {
			return run, nil
		}
	}
	run, err := s.battles.GetBattle(ctx, id)
	if err == nil && s.resultCache != nil && cacheableResult(run, id) {
		// A cache failure must never turn a successful database read into an API failure.
		_ = s.resultCache.Set(ctx, run)
	}
	return run, err
}

func (s *battleService) StopBattle(ctx context.Context, id string) (*BattleRun, error) {
	if !battleUUIDPattern.MatchString(id) {
		return nil, ErrInvalidBattle
	}
	if err := s.authorize(ctx, id); err != nil {
		return nil, err
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
	if err := s.authorize(ctx, id); err != nil {
		return nil, err
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

// ResultCache stores complete snapshots; callers still apply perspective projection.
type ResultCache interface {
	Get(context.Context, string) (*BattleRun, error)
	Set(context.Context, *BattleRun) error
}

func cacheableResult(run *BattleRun, id string) bool {
	return run != nil && run.ID == id && run.Status == "completed" && run.Result != nil
}

func isAdmin(ctx context.Context) bool {
	return hasRole(ctx, authn.Admin)
}

func hasRole(ctx context.Context, role authn.Role) bool {
	a, ok := authn.ActorFromContext(ctx)
	return ok && a.Role == role
}

func (s *battleService) authorize(ctx context.Context, id string) error {
	a, ok := authn.ActorFromContext(ctx)
	if !ok {
		return authn.ErrForbidden
	}
	if a.Role == authn.Admin {
		return nil
	}
	run, err := s.battles.GetBattle(ctx, id)
	if err != nil {
		return err
	}
	return authn.AuthorizeOwner(ctx, run.OwnerID)
}
