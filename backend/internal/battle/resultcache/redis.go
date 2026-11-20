// Package resultcache implements an optional, disposable Redis result cache.
package resultcache

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/observability/metrics"
)

// keyPrefix versions cached entries. Bump it whenever service.BattleRun's JSON shape
// changes, so entries written by the previous shape are ignored instead of decoding
// with missing fields until their TTL expires.
const keyPrefix = "battlefield:completed-battle:v1:"

type Cache struct {
	client  *redis.Client
	ttl     time.Duration
	timeout time.Duration
}

// New does not connect: Redis availability is never an API startup requirement.
// Use a dedicated Redis database per environment; URL credentials are never logged.
func New(url string, ttl, timeout time.Duration) (*Cache, error) {
	if url == "" {
		return &Cache{}, nil
	}
	if ttl <= 0 || timeout <= 0 {
		return nil, errors.New("result cache TTL and timeout must be positive")
	}
	options, err := redis.ParseURL(url)
	if err != nil {
		return nil, errors.New("invalid result cache Redis URL")
	}
	options.DialTimeout = timeout
	options.ReadTimeout = timeout
	options.WriteTimeout = timeout
	options.PoolTimeout = timeout
	options.ContextTimeoutEnabled = true
	options.MaxRetries = -1
	metrics.InitResultCacheSeries()
	return &Cache{client: redis.NewClient(options), ttl: ttl, timeout: timeout}, nil
}

func (c *Cache) Close() error {
	if c.client == nil {
		return nil
	}
	return c.client.Close()
}

func (c *Cache) Get(ctx context.Context, id string) (*service.BattleRun, error) {
	if c.client == nil {
		return nil, nil
	}
	run, err := c.get(ctx, id)
	switch {
	case err != nil:
		metrics.ObserveResultCache("get", "error")
	case run == nil:
		metrics.ObserveResultCache("get", "miss")
	default:
		metrics.ObserveResultCache("get", "hit")
	}
	return run, err
}

func (c *Cache) get(ctx context.Context, id string) (*service.BattleRun, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	data, err := c.client.Get(ctx, keyPrefix+id).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var run service.BattleRun
	if err := json.Unmarshal(data, &run); err != nil {
		return nil, err
	}
	if run.ID != id || run.Status != "completed" || run.Result == nil {
		return nil, errors.New("invalid cached completed battle")
	}
	return &run, nil
}

func (c *Cache) Set(ctx context.Context, run *service.BattleRun) error {
	if c.client == nil {
		return nil
	}
	if run == nil || run.Status != "completed" || run.Result == nil {
		return errors.New("only completed battle results can be cached")
	}
	data, err := json.Marshal(run)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	if err := c.client.Set(ctx, keyPrefix+run.ID, data, c.ttl).Err(); err != nil {
		metrics.ObserveResultCache("set", "error")
		return err
	}
	metrics.ObserveResultCache("set", "stored")
	return nil
}
