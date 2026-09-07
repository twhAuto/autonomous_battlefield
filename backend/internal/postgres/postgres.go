// Configures PostgreSQL connection pools and bounded startup retries with context-aware
// cancellation.

package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Options struct {
	MaxConnections        int32
	MinConnections        int32
	MaxConnectionLifetime time.Duration
}

func NewPostgresDB(ctx context.Context, url string, options Options) (*pgxpool.Pool, error) {
	poolConfig, err := postgresPoolConfig(url, options)
	if err != nil {
		return nil, err
	}
	return newPostgresDB(ctx, poolConfig)
}

// NewPostgresDBWithRetry waits for PostgreSQL while the process remains alive
// and responsive to cancellation. Configuration errors are returned
// immediately; transient connection errors are retried until ctx is done.
func NewPostgresDBWithRetry(
	ctx context.Context,
	url string,
	options Options,
	attemptTimeout time.Duration,
	retryInterval time.Duration,
	onRetry func(attempt int, err error),
) (*pgxpool.Pool, error) {
	poolConfig, err := postgresPoolConfig(url, options)
	if err != nil {
		return nil, err
	}
	if attemptTimeout <= 0 {
		return nil, fmt.Errorf("PostgreSQL attempt timeout must be greater than zero")
	}
	if retryInterval <= 0 {
		return nil, fmt.Errorf("PostgreSQL retry interval must be greater than zero")
	}

	for attempt := 1; ; attempt++ {
		attemptContext, cancel := context.WithTimeout(ctx, attemptTimeout)
		pool, connectErr := newPostgresDB(attemptContext, poolConfig.Copy())
		cancel()
		if connectErr == nil {
			return pool, nil
		}
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("wait for PostgreSQL: %w", err)
		}
		if onRetry != nil {
			onRetry(attempt, connectErr)
		}

		timer := time.NewTimer(retryInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil, fmt.Errorf("wait for PostgreSQL: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

func postgresPoolConfig(url string, options Options) (*pgxpool.Config, error) {
	poolConfig, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse PostgreSQL configuration: %w", err)
	}

	poolConfig.MaxConns = options.MaxConnections
	poolConfig.MinConns = options.MinConnections
	poolConfig.MaxConnLifetime = options.MaxConnectionLifetime
	return poolConfig, nil
}

func newPostgresDB(ctx context.Context, poolConfig *pgxpool.Config) (*pgxpool.Pool, error) {
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("create PostgreSQL pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping PostgreSQL: %w", err)
	}

	return pool, nil
}
