// Shared setup for the migration tests, which each migrate a private schema.

package migrations_test

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/postgres"
)

// testDatabaseConfig skips the test unless the isolated PostgreSQL harness is running.
func testDatabaseConfig(t *testing.T) *pgx.ConnConfig {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	config, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	return config
}

func randomUUID(t *testing.T) string {
	t.Helper()
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		t.Fatalf("rand.Read() returned an error: %v", err)
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		bytes[0:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:16])
}

// migratedSchema returns a pool on a private schema migrated to the latest version.
func migratedSchema(t *testing.T) *pgxpool.Pool {
	t.Helper()
	db, config := privateSchema(t)
	provider, err := postgres.NewMigrationProvider(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	poolConfig, err := pgxpool.ParseConfig(config.ConnString())
	if err != nil {
		t.Fatal(err)
	}
	poolConfig.ConnConfig.RuntimeParams["search_path"] = config.RuntimeParams["search_path"]
	pool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func mustExec(t *testing.T, db *pgxpool.Pool, query string, args ...any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		t.Fatal(err)
	}
}
