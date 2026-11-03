package postgres

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/pressly/goose/v3/lock"
)

// migrationLockBudget bounds each test, including waits for the global migration lock,
// which other test packages hold while they migrate their own schemas in parallel.
const migrationLockBudget = 2 * time.Minute

func TestMigrationProviderLocksAcrossSchemas(t *testing.T) {
	schemaURL, schema, admin := privateSchemaURL(t)
	ctx, cancel := context.WithTimeout(t.Context(), migrationLockBudget)
	defer cancel()
	db, err := sql.Open("pgx", schemaURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	provider, err := NewMigrationProvider(db)
	if err != nil {
		t.Fatal(err)
	}

	// Hold the startup lock on a different connection and search_path. The test
	// provider must respect it before applying migrations. Goose may initialize
	// its schema-local version table when checking for pending migrations. If an
	// assertion fails, closing the admin pool at cleanup releases the lock.
	connection, err := admin.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		t.Fatal(err)
	}
	if err := locker.SessionLock(ctx, connection); err != nil {
		t.Fatal(err)
	}
	blockedContext, cancelBlocked := context.WithTimeout(ctx, 250*time.Millisecond)
	_, err = provider.UpTo(blockedContext, 4)
	cancelBlocked()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("migration with another runner holding the lock = %v, want deadline exceeded", err)
	}
	var tables int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM pg_tables WHERE schemaname=$1 AND tablename <> 'goose_db_version'", schema).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 0 {
		t.Fatalf("blocked migration created %d application tables", tables)
	}
	if err := locker.SessionUnlock(ctx, connection); err != nil {
		t.Fatal(err)
	}
	results, err := provider.UpTo(ctx, 4)
	if err != nil || len(results) != 4 {
		t.Fatalf("migration after releasing the lock applied %d versions: %v", len(results), err)
	}
}

func TestMigrateAppliesPendingMigrationsOnce(t *testing.T) {
	schemaURL, schema, admin := privateSchemaURL(t)
	ctx, cancel := context.WithTimeout(t.Context(), migrationLockBudget)
	defer cancel()
	db, err := sql.Open("pgx", schemaURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	provider, err := NewMigrationProvider(db)
	if err != nil {
		t.Fatal(err)
	}

	applied, err := Migrate(ctx, schemaURL, 5*time.Second)
	if err != nil || applied != len(provider.ListSources()) {
		t.Fatalf("first Migrate applied %d of %d migrations: %v", applied, len(provider.ListSources()), err)
	}
	var factions int
	if err := admin.QueryRowContext(ctx, "SELECT count(*) FROM pg_tables WHERE schemaname=$1 AND tablename='factions'", schema).Scan(&factions); err != nil || factions != 1 {
		t.Fatalf("factions table in %s: %d, %v", schema, factions, err)
	}
	if applied, err = Migrate(ctx, schemaURL, 5*time.Second); err != nil || applied != 0 {
		t.Fatalf("second Migrate applied %d migrations: %v", applied, err)
	}
}
