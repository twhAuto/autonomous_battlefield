// Exercises the reversible removal of the duplicate battle-events index.

package migrations_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/postgres"
)

func TestDuplicateEventIndexMigrationUpAndDown(t *testing.T) {
	ctx := context.Background()
	config := testDatabaseConfig(t)
	admin := stdlib.OpenDB(*config)
	defer admin.Close()
	schema := "event_index_" + strings.ReplaceAll(randomUUID(t), "-", "")
	_, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	config.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*config)
	defer db.Close()
	provider, err := postgres.NewMigrationProvider(db)
	if err != nil {
		t.Fatal(err)
	}

	steps := []struct {
		migrate   func() error
		duplicate bool
	}{
		{func() error { _, err := provider.UpTo(ctx, 14); return err }, true},
		{func() error { _, err := provider.UpTo(ctx, 15); return err }, false},
		{func() error { _, err := provider.Down(ctx); return err }, true},
		{func() error { _, err := provider.UpTo(ctx, 15); return err }, false},
	}
	for i, step := range steps {
		if step.migrate() != nil {
			t.Fatalf("step %d migration failed", i)
		}
		if got := indexExists(t, db, schema, "battle_events_cursor_idx"); got != step.duplicate {
			t.Fatalf("step %d: duplicate index present = %t, want %t", i, got, step.duplicate)
		}
		if !indexExists(t, db, schema, "battle_events_pkey") {
			t.Fatalf("step %d: primary key index missing", i)
		}
	}
}

func indexExists(t *testing.T, db *sql.DB, schema, name string) bool {
	t.Helper()
	var count int
	err := db.QueryRow("SELECT count(*) FROM pg_indexes WHERE schemaname=$1 AND indexname=$2", schema, name).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	return count == 1
}
