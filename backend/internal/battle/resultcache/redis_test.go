// Checks result-cache round trips, expiry, corrupt entries, disabled configuration, and
// failures using an in-process Redis server.

package resultcache

import (
	"context"
	"github.com/alicebob/miniredis/v2"
	"github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"strings"
	"testing"
	"time"
)

func newTestCache(t *testing.T) (*miniredis.Miniredis, *Cache) {
	t.Helper()
	server := miniredis.RunT(t)
	cache, err := New("redis://"+server.Addr(), time.Hour, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cache.Close() })
	return server, cache
}

func TestRoundTripAndExpiry(t *testing.T) {
	server, cache := newTestCache(t)
	ctx := context.Background()
	run := &service.BattleRun{ID: "battle", Status: "completed", Attempt: 2, CurrentTick: 42, Result: &game.Result{}}
	if got, err := cache.Get(ctx, run.ID); err != nil || got != nil {
		t.Fatalf("miss: %v %v", got, err)
	}
	if err := cache.Set(ctx, run); err != nil {
		t.Fatal(err)
	}
	got, err := cache.Get(ctx, run.ID)
	if err != nil || got == nil || got.Attempt != 2 || got.CurrentTick != 42 || got.Result == nil {
		t.Fatalf("round trip: %v %v", got, err)
	}
	got.CurrentTick = 99
	again, err := cache.Get(ctx, run.ID)
	if err != nil || again == nil {
		t.Fatalf("second read: %v %v", again, err)
	}
	if again.CurrentTick != 42 {
		t.Fatal("cached value was mutated")
	}
	if server.TTL(keyPrefix+run.ID) != time.Hour {
		t.Fatal("missing TTL")
	}
	server.FastForward(time.Hour)
	if got, err := cache.Get(ctx, run.ID); err != nil || got != nil {
		t.Fatalf("expired: %v %v", got, err)
	}
}

func TestRejectsCorruptReads(t *testing.T) {
	server, cache := newTestCache(t)
	ctx := context.Background()
	const id = "battle"
	for _, data := range []string{"{", `null`, `{"id":"other","status":"completed","result":{}}`, `{"id":"battle","status":"running","result":{}}`, `{"id":"battle","status":"completed"}`} {
		if err := server.Set(keyPrefix+id, data); err != nil {
			t.Fatal(err)
		}
		if _, err := cache.Get(ctx, id); err == nil {
			t.Errorf("accepted corrupt cache: %s", data)
		}
	}
}

func TestRejectsNonResults(t *testing.T) {
	_, cache := newTestCache(t)
	ctx := context.Background()
	for _, invalid := range []*service.BattleRun{nil, {Status: "running"}, {Status: "completed"}} {
		if err := cache.Set(ctx, invalid); err == nil {
			t.Fatal("cached non-result")
		}
	}
}

func TestUnavailableCacheReturnsErrors(t *testing.T) {
	server, cache := newTestCache(t)
	ctx := context.Background()
	run := &service.BattleRun{ID: "battle", Status: "completed", Result: &game.Result{}}
	server.Close()
	if _, err := cache.Get(ctx, run.ID); err == nil {
		t.Fatal("expected unavailable error")
	}
	if err := cache.Set(ctx, run); err == nil {
		t.Fatal("expected write error")
	}
}
func TestDisabledAndInvalidConfiguration(t *testing.T) {
	cache, err := New("", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := cache.Get(context.Background(), "id"); got != nil || err != nil {
		t.Fatal("disabled read")
	}
	if err := cache.Set(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if err := cache.Close(); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		url          string
		ttl, timeout time.Duration
	}{
		{"redis://localhost", 0, time.Second}, {"redis://localhost", time.Hour, 0}, {"https://secret@localhost", time.Hour, time.Second},
	} {
		_, err := New(tt.url, tt.ttl, tt.timeout)
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("invalid/redaction: %v", err)
		}
	}
}
