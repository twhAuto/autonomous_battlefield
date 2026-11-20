// Checks result-cache round trips, expiry, corrupt entries, disabled configuration, and
// failures using an in-process Redis server.

package resultcache

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/game"
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

// cacheOperations reads battlefield_result_cache_operations_total for one label pair.
func cacheOperations(t *testing.T, operation, outcome string) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != "battlefield_result_cache_operations_total" {
			continue
		}
		for _, metric := range family.GetMetric() {
			labels := map[string]string{}
			for _, pair := range metric.GetLabel() {
				labels[pair.GetName()] = pair.GetValue()
			}
			if labels["operation"] == operation && labels["outcome"] == outcome {
				return metric.GetCounter().GetValue()
			}
		}
	}
	return 0
}

func TestOperationsAreCounted(t *testing.T) {
	server, cache := newTestCache(t)
	ctx := context.Background()
	run := &service.BattleRun{ID: "counted", Status: "completed", Result: &game.Result{}}
	before := map[string]float64{}
	for _, key := range [][2]string{{"get", "miss"}, {"get", "hit"}, {"get", "error"}, {"set", "stored"}, {"set", "error"}} {
		before[key[0]+"/"+key[1]] = cacheOperations(t, key[0], key[1])
	}
	_, _ = cache.Get(ctx, run.ID)
	_ = cache.Set(ctx, run)
	_, _ = cache.Get(ctx, run.ID)
	server.Close()
	_, _ = cache.Get(ctx, run.ID)
	_ = cache.Set(ctx, run)
	for key, want := range map[string]float64{"get/miss": 1, "get/hit": 1, "get/error": 1, "set/stored": 1, "set/error": 1} {
		operation, outcome, _ := strings.Cut(key, "/")
		if got := cacheOperations(t, operation, outcome) - before[key]; got != want {
			t.Errorf("%s: counted %v, want %v", key, got, want)
		}
	}
}

func TestDisabledCacheCountsNothing(t *testing.T) {
	cache, err := New("", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	before := cacheOperations(t, "get", "miss")
	if run, err := cache.Get(context.Background(), "any"); run != nil || err != nil {
		t.Fatal(run, err)
	}
	if cacheOperations(t, "get", "miss") != before {
		t.Fatal("disabled cache recorded an operation")
	}
}
