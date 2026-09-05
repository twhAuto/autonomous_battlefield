package service

import (
	"context"
	"errors"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"testing"
)

type fakeResultCache struct {
	run           *BattleRun
	err           error
	reads, writes int
}

func (c *fakeResultCache) Get(context.Context, string) (*BattleRun, error) {
	c.reads++
	return c.run, c.err
}
func (c *fakeResultCache) Set(_ context.Context, run *BattleRun) error {
	c.writes++
	if c.err == nil {
		c.run = run
	}
	return c.err
}

type countingBattleRepo struct {
	fakeBattleRepo
	reads int
}

func (r *countingBattleRepo) GetBattle(ctx context.Context, id string) (*BattleRun, error) {
	r.reads++
	return r.fakeBattleRepo.GetBattle(ctx, id)
}
func TestCompletedResultCache(t *testing.T) {
	const id = "abcdefab-1234-1234-1234-123456789012"
	ctx := context.Background()
	run := &BattleRun{ID: id, Status: "completed", Result: &game.Result{}}
	repo := &countingBattleRepo{fakeBattleRepo: fakeBattleRepo{run: run}}
	cache := &fakeResultCache{}
	svc := NewServiceWithResultCache(repo, nil, nil, 100, cache)
	if _, err := svc.GetBattle(ctx, "bad"); !errors.Is(err, ErrInvalidBattle) || cache.reads != 0 {
		t.Fatal("invalid ID reached cache")
	}
	if got, err := svc.GetBattle(ctx, id); err != nil || got != run || repo.reads != 1 || cache.writes != 1 {
		t.Fatal("miss did not populate")
	}
	if got, err := svc.GetBattle(ctx, "ABCDEFAB-1234-1234-1234-123456789012"); err != nil || got != run || repo.reads != 1 {
		t.Fatal("hit did not skip database")
	}
	cache.err = errors.New("redis unavailable")
	if got, err := svc.GetBattle(ctx, id); err != nil || got != run || repo.reads != 2 {
		t.Fatal("cache errors prevented fallback")
	}
	repo.getErr = ErrBattleNotFound
	if _, err := svc.GetBattle(ctx, id); !errors.Is(err, ErrBattleNotFound) || cache.writes != 2 {
		t.Fatal("database error was masked/cached")
	}
}
func TestOnlyCompletedResultsAreCached(t *testing.T) {
	const id = "abcdefab-1234-1234-1234-123456789012"
	for _, status := range []string{"queued", "running", "failed", "stopped", "completed"} {
		t.Run(status, func(t *testing.T) {
			run := &BattleRun{ID: id, Status: status}
			repo := &countingBattleRepo{fakeBattleRepo: fakeBattleRepo{run: run}}
			cache := &fakeResultCache{run: run}
			svc := NewServiceWithResultCache(repo, nil, nil, 100, cache)
			for range 2 {
				if _, err := svc.GetBattle(context.Background(), id); err != nil {
					t.Fatal(err)
				}
			}
			if repo.reads != 2 || cache.writes != 0 {
				t.Fatal("non-result was cached")
			}
		})
	}
	repo := &countingBattleRepo{fakeBattleRepo: fakeBattleRepo{getErr: ErrBattleNotFound}}
	cache := &fakeResultCache{run: &BattleRun{ID: "other", Status: "completed", Result: &game.Result{}}}
	svc := NewServiceWithResultCache(repo, nil, nil, 100, cache)
	if _, err := svc.GetBattle(context.Background(), id); !errors.Is(err, ErrBattleNotFound) {
		t.Fatal("cross-battle cache hit")
	}
}
