// Runs every migration's Down section and checks the schema rebuilds identically, so
// the recovery path for a bad release is exercised before it is needed.

package migrations_test

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/twh09/autonomous_battlefield/backend/internal/postgres"
)

// roleRenameVersion renames the cluster-wide AI gateway role. Its Down section would
// rename the role under every other test using the same PostgreSQL server, so the round
// trip passes below it only from an earlier version.
const roleRenameVersion = 17

func TestEveryMigrationRunsDownAndRebuildsTheSameSchema(t *testing.T) {
	ctx := context.Background()
	db, config := privateSchema(t)
	schema := config.RuntimeParams["search_path"]
	provider, err := postgres.NewMigrationProvider(db)
	if err != nil {
		t.Fatal(err)
	}

	migrate(t, "up to before the role rename", func() ([]*goose.MigrationResult, error) { return provider.UpTo(ctx, roleRenameVersion-1) })
	before := schemaObjects(t, db, schema)
	migrate(t, "down to empty", func() ([]*goose.MigrationResult, error) { return provider.DownTo(ctx, 0) })
	if left := schemaObjects(t, db, schema); len(left) != 0 {
		t.Fatalf("Down sections left objects behind: %v", left)
	}
	migrate(t, "back up to before the role rename", func() ([]*goose.MigrationResult, error) { return provider.UpTo(ctx, roleRenameVersion-1) })
	if after := schemaObjects(t, db, schema); !slices.Equal(before, after) {
		t.Fatalf("schema differs after a round trip:\nbefore %v\nafter  %v", before, after)
	}

	// Later migrations round-trip on their own, above the role rename.
	migrate(t, "up to latest", func() ([]*goose.MigrationResult, error) { return provider.Up(ctx) })
	latest := schemaObjects(t, db, schema)
	migrate(t, "down to the role rename", func() ([]*goose.MigrationResult, error) { return provider.DownTo(ctx, roleRenameVersion) })
	migrate(t, "up to latest again", func() ([]*goose.MigrationResult, error) { return provider.Up(ctx) })
	if again := schemaObjects(t, db, schema); !slices.Equal(latest, again) {
		t.Fatalf("schema differs after re-applying later migrations:\nbefore %v\nafter  %v", latest, again)
	}
}

func migrate(t *testing.T, step string, run func() ([]*goose.MigrationResult, error)) {
	t.Helper()
	if _, err := run(); err != nil {
		t.Fatalf("%s: %v", step, err)
	}
}

// schemaObjects lists the schema's relations, constraints, functions and triggers by
// kind and name, sorted, leaving out Goose's own version table.
func schemaObjects(t *testing.T, db *sql.DB, schema string) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `
		SELECT CASE c.relkind WHEN 'r' THEN 'table' WHEN 'v' THEN 'view' WHEN 'i' THEN 'index' WHEN 'S' THEN 'sequence' ELSE c.relkind::text END || ' ' || c.relname
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = $1
		UNION ALL
		SELECT 'constraint ' || conrelid::regclass::text || '.' || conname || ' ' || pg_get_constraintdef(con.oid)
		FROM pg_constraint con JOIN pg_namespace n ON n.oid = con.connamespace WHERE n.nspname = $1
		UNION ALL
		SELECT 'function ' || p.proname || ' ' || md5(p.prosrc)
		FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace WHERE n.nspname = $1
		UNION ALL
		SELECT 'trigger ' || tg.tgname || ' ' || pg_get_triggerdef(tg.oid)
		FROM pg_trigger tg JOIN pg_class c ON c.oid = tg.tgrelid JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND NOT tg.tgisinternal
		ORDER BY 1`, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var objects []string
	for rows.Next() {
		var object string
		if err := rows.Scan(&object); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(object, "goose_db_version") {
			objects = append(objects, object)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return objects
}
