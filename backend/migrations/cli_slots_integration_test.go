// Verifies the CLI concurrency range before and after it widens to eight, and that
// rolling back clamps a wider setting.
package migrations_test

import (
	"context"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/postgres"
)

func TestEightCLISlotsMigration(t *testing.T) {
	ctx := context.Background()
	db, _ := privateSchema(t)
	provider, err := postgres.NewMigrationProvider(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 22); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE runtime_policy SET cli_concurrency=8 WHERE id`); err == nil {
		t.Fatal("eight CLI slots accepted before the migration")
	}
	if _, err = provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE runtime_policy SET cli_concurrency=8 WHERE id`); err != nil {
		t.Fatalf("eight CLI slots rejected: %v", err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE runtime_policy SET cli_concurrency=9 WHERE id`); err == nil {
		t.Fatal("nine CLI slots accepted")
	}
	if _, err = provider.DownTo(ctx, 22); err != nil {
		t.Fatal(err)
	}
	var slots int
	if err = db.QueryRowContext(ctx, `SELECT cli_concurrency FROM runtime_policy WHERE id`).Scan(&slots); err != nil || slots != 4 {
		t.Fatalf("rollback kept %d slots: %v", slots, err)
	}
}
