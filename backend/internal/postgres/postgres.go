// Package postgres opens PostgreSQL connection pools, runs the embedded migrations and
// listens for notifications. Repositories depend on its DBTX and Pool interfaces rather
// than on pgx types.
package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Options sizes a connection pool and overrides any pool settings in the URL.
// MaxConnections must be greater than zero.
type Options struct {
	MaxConnections        int32
	MinConnections        int32
	MaxConnectionLifetime time.Duration
}

// Connect opens a pool and pings the database once.
func Connect(ctx context.Context, url string, options Options) (*pgxpool.Pool, error) {
	poolConfig, err := parsePoolConfig(url, options)
	if err != nil {
		return nil, err
	}
	return connect(ctx, poolConfig)
}

// ConnectWithRetry waits for PostgreSQL while the process remains alive
// and responsive to cancellation. Configuration errors are returned
// immediately; transient connection errors are logged as database_connect_retry
// warnings and retried until ctx is done.
func ConnectWithRetry(
	ctx context.Context,
	url string,
	options Options,
	attemptTimeout time.Duration,
	retryInterval time.Duration,
) (*pgxpool.Pool, error) {
	poolConfig, err := parsePoolConfig(url, options)
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
		pool, connectErr := connect(attemptContext, poolConfig.Copy())
		cancel()
		if connectErr == nil {
			return pool, nil
		}
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("wait for PostgreSQL: %w", err)
		}
		slog.WarnContext(ctx, "database_connect_retry", "attempt", attempt, "retry_in", retryInterval, "error", connectErr)

		timer := time.NewTimer(retryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("wait for PostgreSQL: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

func parsePoolConfig(url string, options Options) (*pgxpool.Config, error) {
	poolConfig, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse PostgreSQL configuration: %w", err)
	}

	poolConfig.MaxConns = options.MaxConnections
	poolConfig.MinConns = options.MinConnections
	poolConfig.MaxConnLifetime = options.MaxConnectionLifetime
	return poolConfig, nil
}

func connect(ctx context.Context, poolConfig *pgxpool.Config) (*pgxpool.Pool, error) {
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
