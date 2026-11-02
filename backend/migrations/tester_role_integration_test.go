// Verifies that the saved model-access policy spells the tester role "tester" after the
// migration and "developer" again after rolling back, leaving other profiles alone.
package migrations_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/postgres"
)

func TestTesterRoleMigration(t *testing.T) {
	ctx := context.Background()
	db, _ := privateSchema(t)
	provider, err := postgres.NewMigrationProvider(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 25); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 26); err != nil {
		t.Fatal(err)
	}
	if roles, version := policyRoles(t, db); roles != `{"profiles": {}, "providers": {}}` || version != 0 {
		t.Fatalf("a policy without the role changed: %s version %d", roles, version)
	}
	if _, err = provider.DownTo(ctx, 25); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE model_access_policy SET policy='{"providers":{"openai":true},"profiles":{"cli":{"enabled":true,"allowedRoles":["admin","developer"]},"api":{"enabled":false,"allowedRoles":[]},"old":{"enabled":true,"allowedRoles":null}}}' WHERE id`); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	want := `{"profiles": {"api": {"enabled": false, "allowedRoles": []}, "cli": {"enabled": true, "allowedRoles": ["admin", "tester"]}, "old": {"enabled": true, "allowedRoles": null}}, "providers": {"openai": true}}`
	if roles, version := policyRoles(t, db); roles != want || version != 1 {
		t.Fatalf("migrated policy = %s version %d", roles, version)
	}
	if _, err = provider.DownTo(ctx, 25); err != nil {
		t.Fatal(err)
	}
	if roles, version := policyRoles(t, db); roles != `{"profiles": {"api": {"enabled": false, "allowedRoles": []}, "cli": {"enabled": true, "allowedRoles": ["admin", "developer"]}, "old": {"enabled": true, "allowedRoles": null}}, "providers": {"openai": true}}` || version != 2 {
		t.Fatalf("rolled-back policy = %s version %d", roles, version)
	}
}

func policyRoles(t *testing.T, db *sql.DB) (string, int64) {
	t.Helper()
	var policy string
	var version int64
	if err := db.QueryRowContext(context.Background(), `SELECT policy::text, version FROM model_access_policy WHERE id`).Scan(&policy, &version); err != nil {
		t.Fatal(err)
	}
	return policy, version
}
