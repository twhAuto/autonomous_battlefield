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

	battle "github.com/twh09/autonomous_battlefield/backend/internal/battle/simulation"
	"github.com/twh09/autonomous_battlefield/backend/internal/battle/simulation/controller"
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
	scenario := battle.Scenario{
		ID: "test", RulesVersion: "1", DecisionIntervalTicks: 10, DecisionLeadTicks: 2,
		Rules: battle.CombatRules{MinimumDamage: 1, DefendAttackPercent: 50, DefendDamageTakenPercent: 50},
		Factions: []battle.FactionSetup{
			{ID: "faction-a", Name: "A", Units: []battle.UnitTemplate{{ID: "a", Health: 20, Attack: 10, Speed: 2}}, FallbackPlan: plan, ScriptedPlans: []controller.PlanProposal{plan}},
			{ID: "faction-b", Name: "B", Units: []battle.UnitTemplate{{ID: "b", Health: 20, Attack: 10, Speed: 1}}, FallbackPlan: plan, ScriptedPlans: []controller.PlanProposal{plan}},
		},
	}
	return &BattleJob{Lease: BattleLease{BattleID: "battle-1", Attempt: 1, Owner: "worker-1"}, Scenario: scenario, RequestedTicks: maxTicks}
}
