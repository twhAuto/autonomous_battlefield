// Checks optional Redis result-cache configuration and rejects invalid values.

package config

import (
	"testing"
	"time"
)

func TestResultCacheConfig(t *testing.T) {
	t.Setenv("BATTLEFIELD_REDIS_URL", "")
	raw := fileConfig{}
	got, err := parseResultCacheConfig(raw)
	if err != nil || got.URL != "" || got.TTL != time.Hour || got.Timeout != 100*time.Millisecond {
		t.Fatalf("defaults: %+v %v", got, err)
	}
	raw.ResultCache.TTL = "30m"
	raw.ResultCache.Timeout = "50ms"
	t.Setenv("BATTLEFIELD_REDIS_URL", " redis://localhost:6379/1 ")
	got, err = parseResultCacheConfig(raw)
	if err != nil || got.URL != "redis://localhost:6379/1" || got.TTL != 30*time.Minute || got.Timeout != 50*time.Millisecond {
		t.Fatalf("custom: %+v %v", got, err)
	}
	for _, value := range []string{"-1s", "0s", "invalid"} {
		raw.ResultCache.TTL = value
		if _, err := parseResultCacheConfig(raw); err == nil {
			t.Fatalf("accepted TTL %q", value)
		}
		raw.ResultCache.TTL = "1h"
		raw.ResultCache.Timeout = value
		if _, err := parseResultCacheConfig(raw); err == nil {
			t.Fatalf("accepted timeout %q", value)
		}
		raw.ResultCache.Timeout = "100ms"
	}
}
