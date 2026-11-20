// Verifies that managed members written before roles existed become Players, and that
// rolling back keeps every audit row the earlier schema can hold.

package migrations_test

import (
	"context"
	"testing"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/postgres"
)

func TestMemberRolesMigration(t *testing.T) {
	ctx := context.Background()
	db, _ := privateSchema(t)
	provider, err := postgres.NewMigrationProvider(db)
	if err != nil {
		t.Fatal(err)
	}
	step := func(_ any, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	step(provider.UpTo(ctx, 27))
	var id string
	if err = db.QueryRowContext(ctx, `INSERT INTO player_members(email,desired_access) VALUES('friend@example.com',true) RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	step(db.ExecContext(ctx, `INSERT INTO player_membership_audit(member_id,actor_id,action) VALUES($1,'admin','grant')`, id))
	step(provider.UpTo(ctx, 28))
	var role string
	if err = db.QueryRowContext(ctx, `SELECT role FROM player_members WHERE id=$1`, id).Scan(&role); err != nil || role != "player" {
		t.Fatalf("existing member role = %q, %v", role, err)
	}
	step(db.ExecContext(ctx, `INSERT INTO player_membership_audit(member_id,actor_id,action,role) VALUES($1,'admin','role','tester')`, id))
	step(provider.DownTo(ctx, 27))
	var actions string
	if err = db.QueryRowContext(ctx, `SELECT string_agg(action,',' ORDER BY id) FROM player_membership_audit WHERE member_id=$1`, id).Scan(&actions); err != nil || actions != "grant" {
		t.Fatalf("rolled-back audit = %q, %v", actions, err)
	}
}
