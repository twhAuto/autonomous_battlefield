// Provides test database connections on private schemas for the integration tests.

package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// testDatabaseURL returns TEST_DATABASE_URL or skips the test when it is not set.
func testDatabaseURL(t *testing.T) string {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	return databaseURL
}

// privateSchemaURL creates an empty schema, dropped after the test, and returns a URL
// whose connections use it, plus an admin connection on the default search path.
func privateSchemaURL(t *testing.T) (string, string, *sql.DB) {
	t.Helper()
	databaseURL := testDatabaseURL(t)
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	admin := stdlib.OpenDB(*config)
	t.Cleanup(func() { _ = admin.Close() })
	schema := fmt.Sprintf("postgres_test_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(t.Context(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal("test database URL is not a URL")
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return parsed.String(), schema, admin
}
