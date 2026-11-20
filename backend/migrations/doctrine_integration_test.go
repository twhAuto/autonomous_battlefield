// Checks the doctrine migration backfills faction profiles without rewriting historical
// battle snapshots.

package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/faction"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/postgres"
)

func TestDoctrineMigrationBackfillsWithoutRewritingHistory(t *testing.T) {
	config := testDatabaseConfig(t)
	admin := stdlib.OpenDB(*config)
	defer admin.Close()
	ctx := context.Background()
	schema := "doctrine_" + strings.ReplaceAll(randomUUID(t), "-", "")
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.ExecContext(ctx, `DROP SCHEMA `+schema+` CASCADE`)
	config.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*config)
	defer db.Close()
	provider, err := postgres.NewMigrationProvider(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 7); err != nil {
		t.Fatal(err)
	}
	id := randomUUID(t)
	blueprint := `{"units":[{"type":"ranger","count":1}],"upgradeIds":["arsenal"],"recruitmentPriority":["ranger"]}`
	if _, err = db.ExecContext(ctx, `INSERT INTO factions(id,name,ruleset_id,blueprint) VALUES($1,'Original','castle-siege-v1',$2)`, id, blueprint); err != nil {
		t.Fatal(err)
	}
	var before string
	if err = db.QueryRowContext(ctx, `SELECT blueprint::text FROM factions WHERE id=$1`, id).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	var name, doctrine, after, ruleset string
	if err = db.QueryRowContext(ctx, `SELECT name,doctrine,blueprint::text,ruleset_id FROM factions WHERE id=$1`, id).Scan(&name, &doctrine, &after, &ruleset); err != nil {
		t.Fatal(err)
	}
	if name != "Original" || doctrine != faction.BalancedDoctrine || before != after || ruleset != "castle-siege-v1" {
		t.Fatal("migration rewrote existing identity or blueprint")
	}
	if _, err = provider.Up(ctx); err != nil {
		t.Fatal("migration rerun", err)
	}
}
