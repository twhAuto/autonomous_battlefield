// Runs every migration's Down section and checks it restores exactly the schema the
// previous migration left, so the recovery path for a bad release is exercised before
// it is needed.

package migrations_test

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/postgres"
)

// roleRenameVersion renames the cluster-wide AI gateway role. Its Down section would
// rename the role under every other test using the same PostgreSQL server, so the
// earlier migrations are taken down only from the version before it.
const roleRenameVersion = 17

func TestEveryMigrationDownRestoresThePreviousSchema(t *testing.T) {
	db, config := privateSchema(t)
	schema := config.RuntimeParams["search_path"]
	provider, err := postgres.NewMigrationProvider(db)
	if err != nil {
		t.Fatal(err)
	}
	latest := int64(len(provider.ListSources()))
	snapshots := map[int64][]string{0: schemaObjects(t, db, schema)}

	stepUp(t, provider, db, schema, snapshots, roleRenameVersion-1)
	stepDown(t, provider, db, schema, snapshots, 0)
	// Rebuilding must reproduce each recorded version again.
	stepUp(t, provider, db, schema, snapshots, roleRenameVersion-1)

	// Later migrations are checked the same way, above the role rename.
	stepUp(t, provider, db, schema, snapshots, latest)
	stepDown(t, provider, db, schema, snapshots, roleRenameVersion)
	stepUp(t, provider, db, schema, snapshots, latest)
}

// stepUp applies migrations one at a time up to target, recording the schema after
// each one, or requiring it to match a recording from an earlier pass.
func stepUp(t *testing.T, provider *goose.Provider, db *sql.DB, schema string, snapshots map[int64][]string, target int64) {
	t.Helper()
	for {
		version, err := provider.GetDBVersion(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if version >= target {
			return
		}
		if _, err := provider.UpByOne(context.Background()); err != nil {
			t.Fatalf("up from %d: %v", version, err)
		}
		requireSnapshot(t, snapshots, version+1, schemaObjects(t, db, schema), "up")
	}
}

// stepDown runs Down sections one at a time down to target, requiring each to leave
// exactly the schema recorded at the version below it.
func stepDown(t *testing.T, provider *goose.Provider, db *sql.DB, schema string, snapshots map[int64][]string, target int64) {
	t.Helper()
	for {
		version, err := provider.GetDBVersion(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if version <= target {
			return
		}
		if _, err := provider.Down(context.Background()); err != nil {
			t.Fatalf("down from %d: %v", version, err)
		}
		requireSnapshot(t, snapshots, version-1, schemaObjects(t, db, schema), "down")
	}
}

func requireSnapshot(t *testing.T, snapshots map[int64][]string, version int64, got []string, direction string) {
	t.Helper()
	want, recorded := snapshots[version]
	if !recorded {
		snapshots[version] = got
		return
	}
	if !slices.Equal(want, got) {
		t.Fatalf("after %s to version %d the schema differs from the recorded one:\nmissing %v\nextra   %v", direction, version, difference(want, got), difference(got, want))
	}
}

func difference(a, b []string) []string {
	var only []string
	for _, item := range a {
		if !slices.Contains(b, item) {
			only = append(only, item)
		}
	}
	return only
}

// schemaObjects describes the schema's tables, views, sequences, columns (type,
// nullability and default), indexes, constraints, functions and triggers, sorted,
// leaving out Goose's own version table.
func schemaObjects(t *testing.T, db *sql.DB, schema string) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `
		SELECT CASE c.relkind WHEN 'r' THEN 'table ' WHEN 'S' THEN 'sequence ' WHEN 'v' THEN 'view ' || pg_get_viewdef(c.oid) || ' ' ELSE c.relkind::text || ' ' END || c.relname
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = $1 AND c.relkind IN ('r', 'S', 'v')
		UNION ALL
		SELECT 'column ' || c.relname || '.' || a.attname || ' ' || format_type(a.atttypid, a.atttypmod)
			|| CASE WHEN a.attnotnull THEN ' not null' ELSE '' END
			|| COALESCE(' default ' || pg_get_expr(d.adbin, d.adrelid), '')
		FROM pg_attribute a JOIN pg_class c ON c.oid = a.attrelid JOIN pg_namespace n ON n.oid = c.relnamespace
		LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
		WHERE n.nspname = $1 AND c.relkind IN ('r', 'v') AND a.attnum > 0 AND NOT a.attisdropped
		UNION ALL
		SELECT 'index ' || pg_get_indexdef(i.indexrelid)
		FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = $1
		UNION ALL
		SELECT 'constraint ' || conrelid::regclass::text || '.' || conname || ' ' || pg_get_constraintdef(con.oid)
		FROM pg_constraint con JOIN pg_namespace n ON n.oid = con.connamespace WHERE n.nspname = $1
		UNION ALL
		SELECT 'function ' || p.proname || ' ' || md5(p.prosrc)
		FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace WHERE n.nspname = $1
		UNION ALL
		SELECT 'trigger ' || pg_get_triggerdef(tg.oid)
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
			objects = append(objects, strings.ReplaceAll(object, schema+".", ""))
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return objects
}
