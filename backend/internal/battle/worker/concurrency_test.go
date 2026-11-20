// Exercises several claim loops in one worker: battles running at the same time, a
// failing battle beside a healthy one, and shutdown with several battles in flight.

package worker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/battle/queue"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/game"
)

// concurrentRepository is a goroutine-safe queue. Battles named in hold block their
// first tick save until every held battle is running, then until the worker stops.
type concurrentRepository struct {
	mu         sync.Mutex
	pending    []*queue.Job
	claims     map[string]int
	running    map[string]bool
	maxRunning int
	completed  map[string]bool
	requeued   map[string]bool
	hold       map[string]bool
	allHeld    chan struct{}
	failSave   string
	completeCh chan string
	claimErr   error
	// claimErrAfter, when set, holds back claimErr until it is closed.
	claimErrAfter <-chan struct{}
}

func newConcurrentRepository(ids ...string) *concurrentRepository {
	repo := &concurrentRepository{claims: map[string]int{}, running: map[string]bool{}, completed: map[string]bool{},
		requeued: map[string]bool{}, hold: map[string]bool{}, allHeld: make(chan struct{}), completeCh: make(chan string, len(ids))}
	for _, id := range ids {
		job := testJob(100)
		job.Lease.BattleID = id
		repo.pending = append(repo.pending, job)
	}
	return repo
}

func (r *concurrentRepository) ClaimBattle(context.Context, string, time.Duration, int) (*queue.Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.claimErr != nil && len(r.claims) > 0 && closed(r.claimErrAfter) {
		return nil, r.claimErr
	}
	if len(r.pending) == 0 {
		return nil, queue.ErrNoBattleAvailable
	}
	job := r.pending[0]
	r.pending = r.pending[1:]
	r.claims[job.Lease.BattleID]++
	return job, nil
}

func (r *concurrentRepository) SaveTick(ctx context.Context, lease queue.Lease, _ game.Transition, _ time.Duration) error {
	r.mu.Lock()
	if lease.BattleID == r.failSave {
		r.mu.Unlock()
		return errWorkerRepository
	}
	held := r.hold[lease.BattleID]
	if held && !r.running[lease.BattleID] {
		r.running[lease.BattleID] = true
		r.maxRunning = max(r.maxRunning, len(r.running))
		if len(r.running) == len(r.hold) {
			close(r.allHeld)
		}
	}
	r.mu.Unlock()
	if !held {
		return nil
	}
	select {
	case <-r.allHeld:
	case <-time.After(5 * time.Second):
		return errors.New("held battles never ran together")
	}
	<-ctx.Done()
	return nil
}

func (r *concurrentRepository) CompleteBattle(_ context.Context, lease queue.Lease, _ game.Transition) error {
	r.mu.Lock()
	r.completed[lease.BattleID] = true
	r.mu.Unlock()
	r.completeCh <- lease.BattleID
	return nil
}

func (r *concurrentRepository) RequeueBattle(_ context.Context, lease queue.Lease) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requeued[lease.BattleID] = true
	return nil
}

func (r *concurrentRepository) BattlePaused(context.Context, queue.Lease) (bool, error) {
	return false, nil
}
func (r *concurrentRepository) HeartbeatBattle(context.Context, queue.Lease, time.Duration) error {
	return nil
}
func (r *concurrentRepository) FailBattle(context.Context, queue.Lease, string, string) error {
	return nil
}
func (r *concurrentRepository) FailExhaustedBattles(context.Context, int) error { return nil }
func (r *concurrentRepository) ExpireBattles(context.Context) error             { return nil }

func closed(signal <-chan struct{}) bool {
	if signal == nil {
		return true
	}
	select {
	case <-signal:
		return true
	default:
		return false
	}
}

// modelJob is a battle whose first faction waits on a model that never answers. Its
// castles outlast the first decision window at tick 9.
func modelJob(id string) *queue.Job {
	job := testJob(20)
	job.Lease.BattleID = id
	job.Scenario.Ruleset.Buildings.CastleHealth = 1000
	for index := range job.Scenario.Factions {
		job.Scenario.Factions[index].Controller = game.ControllerConfig{Type: ai.ControllerScripted}
	}
	job.Scenario.Factions[0].Controller = game.ControllerConfig{Type: ai.ControllerModel, ProfileID: "profile", Provider: "codex-cli", Runtime: "cli"}
	return job
}

func pendingModels(onModelDecision func()) *decisionBattleRepository {
	return &decisionBattleRepository{fakeBattleRepository: &fakeBattleRepository{}, records: map[string]ai.DecisionRecord{}, onModelDecision: onModelDecision}
}

func concurrentWorker(t *testing.T, repo *concurrentRepository, slots int) *Worker {
	t.Helper()
	config := testWorkerConfig()
	config.Concurrency = slots
	worker, err := New(repo, nil, config)
	if err != nil {
		t.Fatalf("New() returned an error: %v", err)
	}
	return worker
}

func runWorker(ctx context.Context, worker *Worker) <-chan error {
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	return done
}

func waitForRun(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return")
		return nil
	}
}

func TestWorkerRunsBattlesInEverySlotAndRequeuesThemOnShutdown(t *testing.T) {
	repo := newConcurrentRepository("battle-a", "battle-b", "battle-c")
	repo.hold = map[string]bool{"battle-a": true, "battle-b": true, "battle-c": true}
	ctx, cancel := context.WithCancel(context.Background())
	done := runWorker(ctx, concurrentWorker(t, repo, 3))
	select {
	case <-repo.allHeld:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("three slots never ran battles at the same time")
	}
	cancel()
	if err := waitForRun(t, done); err != nil {
		t.Fatalf("Run() returned an error: %v", err)
	}
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if repo.maxRunning != 3 {
		t.Fatalf("battles running together = %d, want 3", repo.maxRunning)
	}
	for _, id := range []string{"battle-a", "battle-b", "battle-c"} {
		if repo.claims[id] != 1 || !repo.requeued[id] {
			t.Fatalf("%s: claims = %d, requeued = %v; want one claim and a requeue", id, repo.claims[id], repo.requeued[id])
		}
	}
}

func TestWorkerKeepsOtherSlotsRunningWhenOneBattleFails(t *testing.T) {
	repo := newConcurrentRepository("battle-broken", "battle-ok")
	repo.pending[1].RequestedTicks = 3
	repo.failSave = "battle-broken"
	ctx, cancel := context.WithCancel(context.Background())
	done := runWorker(ctx, concurrentWorker(t, repo, 2))
	select {
	case id := <-repo.completeCh:
		if id != "battle-ok" {
			t.Fatalf("completed %s, want battle-ok", id)
		}
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("the healthy battle never completed")
	}
	cancel()
	if err := waitForRun(t, done); err != nil {
		t.Fatalf("Run() returned an error: %v", err)
	}
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if repo.completed["battle-broken"] || repo.claims["battle-broken"] != 1 || repo.claims["battle-ok"] != 1 {
		t.Fatalf("claims = %v, completed = %v", repo.claims, repo.completed)
	}
}

func TestWorkerClaimFailureStopsEverySlot(t *testing.T) {
	repo := newConcurrentRepository("battle-a")
	repo.hold = map[string]bool{"battle-a": true}
	repo.claimErr = errWorkerRepository
	err := waitForRun(t, runWorker(context.Background(), concurrentWorker(t, repo, 2)))
	if !errors.Is(err, errWorkerRepository) {
		t.Fatalf("Run() error = %v, want the claim failure", err)
	}
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if !repo.requeued["battle-a"] {
		t.Fatal("the running battle was not requeued when a sibling slot failed")
	}
}

func TestWorkerRequeuesABattleCancelledDuringADecisionWindow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	repo := &fakeBattleRepository{job: modelJob("battle-model")}
	worker, err := New(repo, pendingModels(cancel), testWorkerConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := waitForRun(t, runWorker(ctx, worker)); err != nil {
		t.Fatalf("Run() returned an error: %v", err)
	}
	if !repo.requeued {
		t.Fatal("a battle cancelled while waiting for a model was not requeued")
	}
}

func TestWorkerRequeuesADecisionWindowWhenASiblingClaimFails(t *testing.T) {
	windowOpen := make(chan struct{})
	repo := newConcurrentRepository()
	repo.pending = []*queue.Job{modelJob("battle-model")}
	repo.claimErr, repo.claimErrAfter = errWorkerRepository, windowOpen
	config := testWorkerConfig()
	config.Concurrency = 2
	worker, err := New(repo, pendingModels(func() { close(windowOpen) }), config)
	if err != nil {
		t.Fatal(err)
	}
	if err := waitForRun(t, runWorker(context.Background(), worker)); !errors.Is(err, errWorkerRepository) {
		t.Fatalf("Run() error = %v, want the sibling's claim failure", err)
	}
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if !repo.requeued["battle-model"] {
		t.Fatal("the battle waiting for a model was not requeued when its sibling slot failed")
	}
}
