// Exercises engine probe responses as the worker and database enter and leave readiness.

package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/battle/queue"
	"github.com/twh09/autonomous_battlefield/backend/internal/battle/worker"
	"github.com/twh09/autonomous_battlefield/backend/internal/httpapi"
)

func TestEngineProbesFollowWorkerAndDatabaseLifecycle(t *testing.T) {
	repository := &idleHealthRepository{claimed: make(chan struct{})}
	engineWorker, err := worker.New(repository, nil, worker.Config{
		WorkerID: "health-test", PollInterval: time.Millisecond, LeaseDuration: time.Second,
		HeartbeatInterval: 100 * time.Millisecond, ShutdownTimeout: time.Second, MaxAttempts: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	lifecycle := httpapi.NewLifecycleHandler()
	check := func(path string, want int) {
		t.Helper()
		response := httptest.NewRecorder()
		lifecycle.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != want {
			t.Fatalf("%s = %d, want %d", path, response.Code, want)
		}
	}
	check("/livez", http.StatusOK)
	check("/startupz", http.StatusServiceUnavailable)
	database := &healthDatabase{}
	lifecycle.Activate(http.NotFoundHandler(), database, engineWorker.Ready)
	check("/startupz", http.StatusOK)
	check("/readyz", http.StatusServiceUnavailable)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- engineWorker.Run(ctx) }()
	select {
	case <-repository.claimed:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	check("/readyz", http.StatusOK)
	database.err = errors.New("database unavailable")
	check("/readyz", http.StatusServiceUnavailable)
	check("/livez", http.StatusOK)
	database.err = nil
	check("/readyz", http.StatusOK)
	cancel()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not stop")
	}
	check("/readyz", http.StatusServiceUnavailable)
	check("/livez", http.StatusOK)
}

type idleHealthRepository struct {
	worker.Repository
	claimed chan struct{}
}

func (r *idleHealthRepository) ClaimBattle(ctx context.Context, _ string, _ time.Duration, _ int) (*queue.Job, error) {
	close(r.claimed)
	<-ctx.Done()
	return nil, queue.ErrNoBattleAvailable
}

type healthDatabase struct{ err error }

func (d *healthDatabase) Ping(context.Context) error { return d.err }

func (r *idleHealthRepository) ExpireBattles(context.Context) error             { return nil }
func (r *idleHealthRepository) FailExhaustedBattles(context.Context, int) error { return nil }
