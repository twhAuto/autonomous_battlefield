// Verifies that deleting a member keeps its audit history without the member link,
// and that rolling back keeps those unlinked audit rows.

package migrations_test

import (
	"context"
	"testing"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/postgres"
)

func TestMemberAuditRetentionMigration(t *testing.T) {
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
	step(provider.UpTo(ctx, 30))
	var id string
	if err = db.QueryRowContext(ctx, `INSERT INTO player_members(email,desired_access) VALUES('friend@example.com',false) RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	step(db.ExecContext(ctx, `INSERT INTO player_membership_audit(member_id,actor_id,action) VALUES($1,'admin','grant'),($1,'admin','remove')`, id))
	step(db.ExecContext(ctx, `DELETE FROM player_members WHERE id=$1`, id))
	step(provider.DownTo(ctx, 29))
	var unlinked int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM player_membership_audit WHERE member_id IS NULL AND action IN ('grant','remove')`).Scan(&unlinked); err != nil || unlinked != 2 {
		t.Fatalf("unlinked audit rows after rollback = %d, %v", unlinked, err)
	}
}
