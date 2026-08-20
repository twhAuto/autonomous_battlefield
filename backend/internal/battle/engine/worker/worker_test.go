package worker

import (
	"bytes"
	"context"
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
	job        *BattleJob
	claims     int
	saved      []battle.Transition
	completed  *battle.Transition
	requeued   bool
	onSave     func()
	onComplete func()
}

func (r *fakeBattleRepository) ClaimBattle(context.Context, string, time.Duration, int) (*BattleJob, error) {
	if r.job == nil || r.claims > 0 {
		return nil, ErrNoBattleAvailable
	}
	r.claims++
	return r.job, nil
}

func (r *fakeBattleRepository) SaveTick(_ context.Context, _ BattleLease, transition battle.Transition, _ time.Duration) error {
	r.saved = append(r.saved, transition)
	if r.onSave != nil {
		r.onSave()
		r.onSave = nil
	}
	return nil
}

func (r *fakeBattleRepository) CompleteBattle(_ context.Context, _ BattleLease, transition battle.Transition) error {
	r.completed = &transition
	if r.onComplete != nil {
		r.onComplete()
	}
	return nil
}

func (r *fakeBattleRepository) HeartbeatBattle(context.Context, BattleLease, time.Duration) error {
	return nil
}

func (r *fakeBattleRepository) RequeueBattle(context.Context, BattleLease) error {
	r.requeued = true
	return nil
}

func (r *fakeBattleRepository) FailBattle(context.Context, BattleLease, string, string) error {
	return nil
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
