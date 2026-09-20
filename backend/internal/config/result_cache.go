// Parses and validates result cache configuration.
package config

import (
	"os"
	"strings"
	"time"
)

type ResultCache struct {
	URL     string
	TTL     time.Duration
	Timeout time.Duration
}

func parseResultCacheConfig(raw fileConfig) (ResultCache, error) {
	ttl, err := parseOptionalDuration("resultCache.ttl", raw.ResultCache.TTL, time.Hour, false)
	if err != nil {
		return ResultCache{}, err
	}
	timeout, err := parseOptionalDuration("resultCache.timeout", raw.ResultCache.Timeout, 100*time.Millisecond, false)
	if err != nil {
		return ResultCache{}, err
	}
	return ResultCache{URL: strings.TrimSpace(os.Getenv(environmentVariableName(raw.ResultCache.URLEnvironmentVariable, "BATTLEFIELD_REDIS_URL"))), TTL: ttl, Timeout: timeout}, nil
}
