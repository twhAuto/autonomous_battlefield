package worker

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/battle/simulation/controller"
	battle "github.com/twh09/autonomous_battlefield/backend/internal/game"
)

func TestWorkerCompletesClaimedBattle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	repo := &fakeBattleRepository{job: testJob(1)}
	repo.onComplete = cancel
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	worker, err := NewWithLogger(repo, testWorkerConfig(), logger)
	if err != nil {
		t.Fatalf("New() returned an error: %v", err)
	}

	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run() returned an error: %v", err)
	}
	if repo.completed == nil || repo.completed.Result == nil {
		t.Fatalf("completed transition = %#v, want a result", repo.completed)
	}
	if repo.claims != 1 {
		t.Fatalf("claims = %d, want 1", repo.claims)
	}
	for _, message := range []string{"battle_worker_ready", "battle_claimed", "battle_completed", "battle_worker_stopped"} {
		if !strings.Contains(logs.String(), `"msg":"`+message+`"`) {
			t.Fatalf("logs do not contain %q: %s", message, logs.String())
		}
	}
}

func TestWorkerRequeuesAtTickBoundaryOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	repo := &fakeBattleRepository{job: testJob(100)}
	repo.onSave = cancel
	worker, err := New(repo, testWorkerConfig())
	if err != nil {
		t.Fatalf("New() returned an error: %v", err)
	}

	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run() returned an error: %v", err)
	}
	if !repo.requeued {
		t.Fatal("battle was not requeued during shutdown")
	}
}

func TestNewWorkerRejectsInvalidConfiguration(t *testing.T) {
	valid := testWorkerConfig()
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "worker ID", mutate: func(config *Config) { config.WorkerID = "" }},
		{name: "poll interval", mutate: func(config *Config) { config.PollInterval = 0 }},
		{name: "lease duration", mutate: func(config *Config) { config.LeaseDuration = 0 }},
		{name: "heartbeat interval", mutate: func(config *Config) { config.HeartbeatInterval = 0 }},
		{name: "heartbeat at lease duration", mutate: func(config *Config) { config.HeartbeatInterval = config.LeaseDuration }},
		{name: "shutdown timeout", mutate: func(config *Config) { config.ShutdownTimeout = 0 }},
		{name: "max attempts", mutate: func(config *Config) { config.MaxAttempts = 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			configuration := valid
			test.mutate(&configuration)
			if _, err := NewWithLogger(&fakeBattleRepository{}, configuration, nil); err == nil {
				t.Fatal("NewWithLogger() accepted invalid configuration")
			}
		})
	}

	if _, err := NewWithLogger(&fakeBattleRepository{}, valid, nil); err != nil {
		t.Fatalf("NewWithLogger() rejected a nil logger: %v", err)
	}
}

func TestWorkerWaitsWhilePausedAndHeartbeatsLease(t *testing.T) {
	repository := &fakeBattleRepository{pausedChecks: 2}
	config := testWorkerConfig()
	config.HeartbeatInterval = time.Millisecond
	config.LeaseDuration = 10 * time.Millisecond
	worker, err := New(repository, config)
	if err != nil {
		t.Fatal(err)
	}
	pausedFor, err := worker.waitWhilePaused(context.Background(), BattleLease{BattleID: "battle-1", Attempt: 1, Owner: "worker-1"})
	if err != nil || pausedFor <= 0 || repository.heartbeats == 0 {
		t.Fatalf("pausedFor=%v heartbeats=%d err=%v", pausedFor, repository.heartbeats, err)
	}
}

func TestWorkerRunStopsOnCancellationAndClaimFailure(t *testing.T) {
	t.Run("already cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		worker, _ := New(&fakeBattleRepository{}, testWorkerConfig())
		if err := worker.Run(ctx); err != nil {
			t.Fatalf("Run() returned an error: %v", err)
		}
		if worker.Ready() {
			t.Fatal("worker remained ready after cancellation")
		}
	})

	t.Run("claim error", func(t *testing.T) {
		worker, _ := New(&fakeBattleRepository{claimErr: errWorkerRepository}, testWorkerConfig())
		err := worker.Run(context.Background())
		if !errors.Is(err, errWorkerRepository) {
			t.Fatalf("Run() error = %v, want repository error", err)
		}
	})
}

func TestWorkerProcessFailurePaths(t *testing.T) {
	t.Run("invalid scenario is marked failed", func(t *testing.T) {
		repository := &fakeBattleRepository{}
		worker, _ := New(repository, testWorkerConfig())
		job := &BattleJob{Lease: BattleLease{BattleID: "bad", Owner: "worker-1"}, Scenario: battle.Scenario{}}
		if err := worker.process(context.Background(), job); err != nil {
			t.Fatalf("process() returned an error: %v", err)
		}
		if repository.failedCode != "invalid_scenario" {
			t.Fatalf("failure code = %q, want invalid_scenario", repository.failedCode)
		}
	})

	t.Run("invalid scenario failure cannot be persisted", func(t *testing.T) {
		repository := &fakeBattleRepository{failErr: errWorkerRepository}
		worker, _ := New(repository, testWorkerConfig())
		err := worker.process(context.Background(), &BattleJob{Lease: BattleLease{BattleID: "bad"}})
		if !errors.Is(err, errWorkerRepository) {
			t.Fatalf("process() error = %v, want repository error", err)
		}
	})

	t.Run("save tick", func(t *testing.T) {
		repository := &fakeBattleRepository{saveErr: errWorkerRepository}
		worker, _ := New(repository, testWorkerConfig())
		err := worker.process(context.Background(), testJob(100))
		if !errors.Is(err, errWorkerRepository) {
			t.Fatalf("process() error = %v, want repository error", err)
		}
	})

	t.Run("complete battle", func(t *testing.T) {
		repository := &fakeBattleRepository{completeErr: errWorkerRepository}
		worker, _ := New(repository, testWorkerConfig())
		err := worker.process(context.Background(), testJob(1))
		if !errors.Is(err, errWorkerRepository) {
			t.Fatalf("process() error = %v, want repository error", err)
		}
	})
}

func TestWorkerShutdownHandoffs(t *testing.T) {
	lease := BattleLease{BattleID: "battle-1", Attempt: 1, Owner: "worker-1"}
	transition := battle.Transition{Tick: 2}

	t.Run("persisted tick requeued", func(t *testing.T) {
		repository := &fakeBattleRepository{}
		worker, _ := New(repository, testWorkerConfig())
		err := worker.persistTickAndRequeue(context.Background(), lease, transition)
		if !errors.Is(err, context.Canceled) || !repository.requeued || len(repository.saved) != 1 {
			t.Fatalf("persistTickAndRequeue() error = %v, repository = %#v", err, repository)
		}
	})

	for _, test := range []struct {
		name       string
		repository *fakeBattleRepository
		want       error
	}{
		{name: "save failure", repository: &fakeBattleRepository{saveErr: errWorkerRepository}, want: errWorkerRepository},
		{name: "requeue failure", repository: &fakeBattleRepository{requeueErr: errWorkerRepository}, want: errWorkerRepository},
		{name: "lease lost", repository: &fakeBattleRepository{requeueErr: ErrBattleLeaseLost}, want: ErrBattleLeaseLost},
	} {
		t.Run(test.name, func(t *testing.T) {
			worker, _ := New(test.repository, testWorkerConfig())
			err := worker.persistTickAndRequeue(context.Background(), lease, transition)
			if !errors.Is(err, test.want) {
				t.Fatalf("persistTickAndRequeue() error = %v, want %v", err, test.want)
			}
		})
	}

	for _, test := range []struct {
		name       string
		repository *fakeBattleRepository
		want       error
	}{
		{name: "success", repository: &fakeBattleRepository{}, want: context.Canceled},
		{name: "failure", repository: &fakeBattleRepository{requeueErr: errWorkerRepository}, want: errWorkerRepository},
		{name: "lease lost", repository: &fakeBattleRepository{requeueErr: ErrBattleLeaseLost}, want: ErrBattleLeaseLost},
	} {
		t.Run("requeue "+test.name, func(t *testing.T) {
			worker, _ := New(test.repository, testWorkerConfig())
			err := worker.requeueAfterCancellation(context.Background(), lease)
			if !errors.Is(err, test.want) {
				t.Fatalf("requeueAfterCancellation() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestWorkerWaitsAndHeartbeats(t *testing.T) {
	t.Run("deadline", func(t *testing.T) {
		worker, _ := New(&fakeBattleRepository{}, testWorkerConfig())
		if err := worker.waitUntilNextTick(context.Background(), BattleLease{}, time.Now()); err != nil {
			t.Fatalf("waitUntilNextTick() returned an error: %v", err)
		}
	})

	t.Run("heartbeat", func(t *testing.T) {
		repository := &fakeBattleRepository{}
		configuration := testWorkerConfig()
		configuration.HeartbeatInterval = time.Millisecond
		worker, _ := New(repository, configuration)
		if err := worker.waitUntilNextTick(context.Background(), BattleLease{}, time.Now().Add(3*time.Millisecond)); err != nil {
			t.Fatalf("waitUntilNextTick() returned an error: %v", err)
		}
		if repository.heartbeats == 0 {
			t.Fatal("waitUntilNextTick() did not heartbeat")
		}
	})

	t.Run("heartbeat failure", func(t *testing.T) {
		repository := &fakeBattleRepository{heartbeatErr: errWorkerRepository}
		configuration := testWorkerConfig()
		configuration.HeartbeatInterval = time.Millisecond
		worker, _ := New(repository, configuration)
		err := worker.waitUntilNextTick(context.Background(), BattleLease{}, time.Now().Add(time.Second))
		if !errors.Is(err, errWorkerRepository) {
			t.Fatalf("waitUntilNextTick() error = %v, want repository error", err)
		}
	})

	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		worker, _ := New(&fakeBattleRepository{}, testWorkerConfig())
		if err := worker.waitUntilNextTick(ctx, BattleLease{}, time.Now().Add(time.Second)); !errors.Is(err, context.Canceled) {
			t.Fatalf("waitUntilNextTick() error = %v, want context cancellation", err)
		}
		if wait(ctx, time.Second) {
			t.Fatal("wait() returned true for cancelled context")
		}
	})
}

func TestHealthHandlerReportsReadiness(t *testing.T) {
	repo := &fakeBattleRepository{}
	worker, err := New(repo, testWorkerConfig())
	if err != nil {
		t.Fatalf("New() returned an error: %v", err)
	}
	handler := HealthHandler(worker, fakePinger{})

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("not-ready status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	worker.ready.Store(true)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("ready status = %d, want %d", response.Code, http.StatusOK)
	}
}

type fakePinger struct{ err error }

func (p fakePinger) Ping(context.Context) error { return p.err }

type fakeBattleRepository struct {
	job          *BattleJob
	claims       int
	saved        []battle.Transition
	completed    *battle.Transition
	requeued     bool
	onSave       func()
	onComplete   func()
	claimErr     error
	saveErr      error
	completeErr  error
	heartbeatErr error
	requeueErr   error
	failErr      error
	failedCode   string
	heartbeats   int
	pausedChecks int
}

var errWorkerRepository = errors.New("repository failure")

func (r *fakeBattleRepository) ClaimBattle(context.Context, string, time.Duration, int) (*BattleJob, error) {
	if r.claimErr != nil {
		return nil, r.claimErr
	}
	if r.job == nil || r.claims > 0 {
		return nil, ErrNoBattleAvailable
	}
	r.claims++
	return r.job, nil
}

func (r *fakeBattleRepository) SaveTick(_ context.Context, _ BattleLease, transition battle.Transition, _ time.Duration) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	r.saved = append(r.saved, transition)
	if r.onSave != nil {
		r.onSave()
		r.onSave = nil
	}
	return nil
}

func (r *fakeBattleRepository) CompleteBattle(_ context.Context, _ BattleLease, transition battle.Transition) error {
	if r.completeErr != nil {
		return r.completeErr
	}
	r.completed = &transition
	if r.onComplete != nil {
		r.onComplete()
	}
	return nil
}

func (r *fakeBattleRepository) HeartbeatBattle(context.Context, BattleLease, time.Duration) error {
	r.heartbeats++
	return r.heartbeatErr
}

func (r *fakeBattleRepository) BattlePaused(context.Context, BattleLease) (bool, error) {
	if r.pausedChecks > 0 {
		r.pausedChecks--
		return true, nil
	}
	return false, nil
}

func (r *fakeBattleRepository) RequeueBattle(context.Context, BattleLease) error {
	if r.requeueErr != nil {
		return r.requeueErr
	}
	r.requeued = true
	return nil
}

func (r *fakeBattleRepository) FailBattle(_ context.Context, _ BattleLease, code, _ string) error {
	r.failedCode = code
	return r.failErr
}

func testWorkerConfig() Config {
	return Config{WorkerID: "worker-1", PollInterval: time.Millisecond, LeaseDuration: time.Second, HeartbeatInterval: 100 * time.Millisecond, ShutdownTimeout: time.Second, MaxAttempts: 3}
}

func testJob(maxTicks uint64) *BattleJob {
	plan := controller.PlanProposal{Name: "attack", ActionWeights: map[string]int64{battle.ActionAttack: 1500, battle.ActionDefend: 500}}
	blueprint := battle.Blueprint{Units: []battle.UnitSelection{{Type: "vanguard", Count: 1}}, RecruitmentPriority: []string{"vanguard"}}
	ruleset := battle.Ruleset{ID: "castle-siege-v1", Version: "1", DesignBudget: 120, StartingGold: 60, Units: []battle.UnitType{{ID: "vanguard", Cost: 35, Health: 20, Attack: 10, Defense: 1, Speed: 2, Movement: 2, Range: 1, Vision: 2}}, Upgrades: []battle.Upgrade{{ID: "arsenal", Cost: 20, AttackBonus: 2}}, Buildings: battle.BuildingRules{CastleHealth: 20, CastleDefense: 1, CastleVision: 4, CastleIncome: 10, EconomyCost: 50, EconomyHealth: 60, EconomyDefense: 2, EconomyVision: 2, EconomyIncome: 10, MaximumEconomyCount: 2}, Combat: battle.CombatRules{MinimumDamage: 1, DefendAttackPercent: 50, DefendDamageTakenPercent: 50, LootPercent: 50}}
	gameMap := battle.Map{Kind: "authored", Width: 15, Height: 11, Rows: []string{"...............", ".......#.......", "..b....#....b..", "...............", ".......#.......", "...............", ".......#.......", "...............", "..b....#....b..", ".......#.......", "..............."}, CastleSpawns: [2]battle.Position{{X: 1, Y: 5}, {X: 13, Y: 5}}, EconomySites: [2][]battle.Position{{{X: 2, Y: 2}, {X: 2, Y: 8}}, {{X: 12, Y: 2}, {X: 12, Y: 8}}}}
	scenario := battle.Scenario{
		ID: "test", RulesVersion: "1", RulesetID: ruleset.ID, DecisionIntervalTicks: 10, DecisionLeadTicks: 2, Map: gameMap, Ruleset: ruleset,
		Factions: []battle.FactionSetup{
			{ID: "faction-a", Name: "A", Blueprint: blueprint, FallbackPlan: plan, ScriptedPlans: []controller.PlanProposal{plan}},
			{ID: "faction-b", Name: "B", Blueprint: blueprint, FallbackPlan: plan, ScriptedPlans: []controller.PlanProposal{plan}},
		},
	}
	return &BattleJob{Lease: BattleLease{BattleID: "battle-1", Attempt: 1, Owner: "worker-1"}, Scenario: scenario, RequestedTicks: maxTicks}
}

type decisionBattleRepository struct {
	*fakeBattleRepository
	records            map[string]ai.DecisionRecord
	failModels         bool
	modelErrorCategory string
}

func (r *decisionBattleRepository) CreateOrReuseDecision(_ context.Context, p ai.CreateDecisionParams) (ai.DecisionRecord, error) {
	record := ai.DecisionRecord{ID: p.ID, BattleID: p.BattleID, Attempt: p.Attempt, FactionID: p.FactionID, TargetTick: p.TargetTick, Version: 1, Status: "pending", ControllerType: p.ControllerType, ProfileID: p.ProfileID, Provider: p.Provider, Runtime: p.Runtime, Observation: p.Observation, CreatedAt: time.Now()}
	if p.ControllerType == ai.ControllerModel && r.failModels {
		record.Status = "failed"
		record.ErrorCategory = r.modelErrorCategory
	}
	r.records[record.ID] = record
	return record, nil
}

func TestDecisionWindowStopsWhenBattleWasStopped(t *testing.T) {
	job := testJob(20)
	for index := range job.Scenario.Factions {
		job.Scenario.Factions[index].Controller = battle.ControllerConfig{Type: ai.ControllerScripted}
	}
	job.Scenario.Factions[0].Controller = battle.ControllerConfig{Type: ai.ControllerModel, ProfileID: "profile", Provider: "codex-cli", Runtime: "cli"}
	simulation, err := battle.NewMatch(job.Lease.BattleID, job.Scenario, job.RequestedTicks)
	if err != nil {
		t.Fatal(err)
	}
	for range 9 {
		if _, err := simulation.Advance(); err != nil {
			t.Fatal(err)
		}
	}
	repository := &decisionBattleRepository{fakeBattleRepository: &fakeBattleRepository{}, records: map[string]ai.DecisionRecord{}, failModels: true, modelErrorCategory: "battle_stopped"}
	worker, _ := New(repository, testWorkerConfig())
	if _, err := worker.resolveDecisionWindow(context.Background(), job.Lease, simulation); !errors.Is(err, ErrBattleLeaseLost) {
		t.Fatalf("error = %v, want ErrBattleLeaseLost", err)
	}
	for _, record := range repository.records {
		if record.ControllerType == ai.ControllerModel && record.Status != "failed" {
			t.Fatalf("stopped decision was changed: %#v", record)
		}
	}
}
func (r *decisionBattleRepository) GetDecision(_ context.Context, id string) (ai.DecisionRecord, error) {
	return r.records[id], nil
}
func (r *decisionBattleRepository) ListWindowDecisions(_ context.Context, _ string, _ int, target uint64) ([]ai.DecisionRecord, error) {
	values := []ai.DecisionRecord{}
	for _, record := range r.records {
		if record.TargetTick == target {
			values = append(values, record)
		}
	}
	return values, nil
}
func (r *decisionBattleRepository) SubmitDecisionPlan(_ context.Context, s ai.PlanSubmission) (ai.SubmissionResult, error) {
	record := r.records[s.RequestID]
	record.Status = "accepted"
	record.Version++
	record.ResolvedPlan = &s.Plan
	resolved := time.Now()
	record.ResolvedAt = &resolved
	r.records[s.RequestID] = record
	return ai.SubmissionResult{Accepted: true, Status: "accepted"}, nil
}
func (r *decisionBattleRepository) ResolveDecisionFallback(_ context.Context, id string, _ int64, plan controller.PlanProposal, source, category string) (bool, error) {
	record := r.records[id]
	record.Status = "fallback"
	record.ResolvedPlan = &plan
	record.FallbackSource = source
	record.ErrorCategory = category
	resolved := time.Now()
	record.ResolvedAt = &resolved
	r.records[id] = record
	return true, nil
}
func (r *decisionBattleRepository) ClaimModelDecision(context.Context, string, []string, time.Duration) (ai.DecisionRecord, error) {
	return ai.DecisionRecord{}, ai.ErrNoDecision
}
func (r *decisionBattleRepository) FinishDecisionFailure(context.Context, string, string, ai.Usage, map[string]any) error {
	return nil
}
func (r *decisionBattleRepository) RecordDecisionUsage(context.Context, string, ai.Usage, map[string]any) error {
	return nil
}

func TestDecisionWindowResolvesScriptedAndModelFallback(t *testing.T) {
	for _, model := range []bool{false, true} {
		t.Run(map[bool]string{false: "scripted", true: "model"}[model], func(t *testing.T) {
			job := testJob(20)
			job.Scenario.Ruleset.Buildings.CastleHealth = 100000
			for index := range job.Scenario.Factions {
				job.Scenario.Factions[index].Controller = battle.ControllerConfig{Type: ai.ControllerScripted}
				if model && index == 0 {
					job.Scenario.Factions[index].Controller = battle.ControllerConfig{Type: ai.ControllerModel, ProfileID: "profile", Provider: "openai", Runtime: "api"}
				}
			}
			simulation, err := battle.NewMatch(job.Lease.BattleID, job.Scenario, job.RequestedTicks)
			if err != nil {
				t.Fatal(err)
			}
			for range 9 {
				if _, err := simulation.Advance(); err != nil {
					t.Fatal(err)
				}
			}
			repository := &decisionBattleRepository{fakeBattleRepository: &fakeBattleRepository{}, records: map[string]ai.DecisionRecord{}, failModels: model}
			var logs bytes.Buffer
			worker, _ := NewWithLogger(repository, testWorkerConfig(), slog.New(slog.NewJSONHandler(&logs, nil)))
			plans, err := worker.resolveDecisionWindow(context.Background(), job.Lease, simulation)
			if err != nil || len(plans) != 2 {
				t.Fatalf("plans=%#v err=%v", plans, err)
			}
			if _, err := simulation.AdvanceWithPlans(plans); err != nil {
				t.Fatal(err)
			}
			if simulation.State().Tick != 10 {
				t.Fatal(simulation.State().Tick)
			}
			for _, expected := range []string{`"msg":"decision_window_started"`, `"msg":"decision_prepared"`, `"msg":"decision_resolved"`, `"msg":"decision_window_completed"`} {
				if !strings.Contains(logs.String(), expected) {
					t.Fatalf("logs do not contain %s: %s", expected, logs.String())
				}
			}
			if model && (!strings.Contains(logs.String(), `"decision_status":"fallback"`) || !strings.Contains(logs.String(), `"error_category":"timeout"`)) {
				t.Fatalf("fallback fields missing: %s", logs.String())
			}
		})
	}
}
