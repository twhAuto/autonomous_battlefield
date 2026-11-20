// Runs embedded Goose migrations through a database connection separate from application
// pool startup.

package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
	"github.com/twhAuto/autonomous_battlefield/backend/migrations"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Migrate applies all pending database migrations and returns the number that
// were applied. Migration files are embedded in the application binary.
func Migrate(ctx context.Context, url string, connectTimeout time.Duration) (int, error) {
	db, err := sql.Open("pgx", url)
	if err != nil {
		return 0, fmt.Errorf("open PostgreSQL connection: %w", err)
	}
	defer db.Close()

	connectionContext, cancelConnection := context.WithTimeout(ctx, connectTimeout)
	err = db.PingContext(connectionContext)
	cancelConnection()
	if err != nil {
		return 0, fmt.Errorf("ping PostgreSQL: %w", err)
	}

	provider, err := NewMigrationProvider(db)
	if err != nil {
		return 0, fmt.Errorf("create migration provider: %w", err)
	}

	results, err := provider.Up(ctx)
	if err != nil {
		return 0, fmt.Errorf("apply migrations: %w", err)
	}
	return len(results), nil
}

// NewMigrationProvider uses the same database-wide lock for startup and migration
// tests. Separate schemas still share roles and public-schema grants.
func NewMigrationProvider(db *sql.DB) (*goose.Provider, error) {
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, fmt.Errorf("create migration lock: %w", err)
	}
	return goose.NewProvider(goose.DialectPostgres, db, migrations.Files, goose.WithSessionLocker(locker))
}
