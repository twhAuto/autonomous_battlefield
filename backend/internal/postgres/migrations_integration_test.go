package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3/lock"
)

func TestMigrationProviderLocksAcrossSchemas(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	config, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	admin := stdlib.OpenDB(*config)
	// Closing this dedicated pool also releases the session lock if an assertion fails.
	defer admin.Close()
	schema := fmt.Sprintf("migration_lock_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	config = config.Copy()
	config.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*config)
	defer db.Close()
	provider, err := NewMigrationProvider(db)
	if err != nil {
		t.Fatal(err)
	}

	// Hold the startup lock on a different connection and search_path. The test
	// provider must respect it before applying migrations. Goose may initialize
	// its schema-local version table when checking for pending migrations.
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
