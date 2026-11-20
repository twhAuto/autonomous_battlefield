// Verifies that the people table enforces the display-name rules and its role values,
// and that rolling back removes it.

package migrations_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/postgres"
)

func TestPeopleMigration(t *testing.T) {
	ctx := context.Background()
	db, _ := privateSchema(t)
	provider, err := postgres.NewMigrationProvider(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 29); err != nil {
		t.Fatal(err)
	}
	insert := func(actor, name, role string) error {
		_, err := db.ExecContext(ctx, `INSERT INTO people(actor_id,display_name,role) VALUES($1,NULLIF($2,''),$3)`, actor, name, role)
		return err
	}
	for _, accepted := range []struct{ actor, name, role string }{
		{"a", "Ada", "player"}, {"b", "Grace_Hopper-24 chars ok", "tester"}, {"c", "", "admin"}, {"d", "", "player"},
	} {
		if err = insert(accepted.actor, accepted.name, accepted.role); err != nil {
			t.Fatalf("%+v rejected: %v", accepted, err)
		}
	}
	for _, rejected := range []struct{ actor, name, role string }{
		{"e", "ADA", "player"}, {"f", "Al", "player"}, {"g", " Ada Lovelace", "player"}, {"h", "Ada!", "player"},
		{"i", "Twenty-five characters ok", "player"}, {"j", "Guest", "guest"}, {"k", "Ada ", "player"},
	} {
		if insert(rejected.actor, rejected.name, rejected.role) == nil {
			t.Fatalf("%+v accepted", rejected)
		}
	}
	if _, err = provider.DownTo(ctx, 28); err != nil {
		t.Fatal(err)
	}
	if tableExists(t, db, "people") {
		t.Fatal("people remains after rollback")
	}
}

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var exists bool
	if err := db.QueryRowContext(context.Background(), `SELECT to_regclass($1) IS NOT NULL`, name).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	return exists
}
