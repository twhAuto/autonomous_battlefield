// Package database opens the PostgreSQL pool for the binaries that use one. It is
// separate from startup so the agent, which has no database access, never imports it.
package database

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/config"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/postgres"
)

// Connect opens the configured pool, retrying every second until ctx is done.
func Connect(ctx context.Context, database config.Database) (*pgxpool.Pool, error) {
	return postgres.ConnectWithRetry(ctx, database.URL, postgres.Options{
		MaxConnections:        database.MaxConnections,
		MinConnections:        database.MinConnections,
		MaxConnectionLifetime: database.MaxConnectionLifetime,
	}, database.ConnectTimeout, time.Second)
}
