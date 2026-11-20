// Checks the faction background migration (00009) keeps existing profiles readable and
// editable and leaves historical battle snapshots untouched.

package migrations_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/faction"
	factionrepo "github.com/twhAuto/autonomous_battlefield/backend/internal/faction/repo"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/postgres"
)

func TestFactionBackgroundMigrationPreservesProfilesAndSnapshots(t *testing.T) {
	ctx := context.Background()
	db, config := privateSchema(t)
	provider, err := postgres.NewMigrationProvider(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 8); err != nil {
		t.Fatal(err)
	}
	id := randomUUID(t)
	before := seedLegacyFaction(t, db, id)

	if _, err = provider.UpTo(ctx, 9); err != nil {
		t.Fatal(err)
	}
	var background string
	if err = db.QueryRowContext(ctx, `SELECT background FROM factions WHERE id=$1`, id).Scan(&background); err != nil || background != "" {
		t.Fatalf("background after 00009 = %q, %v; want empty", background, err)
	}
	if _, err = provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if after := battleSnapshot(t, db, id); after != before {
		t.Fatalf("historical snapshot rewritten:\n%s\n%s", before, after)
	}
	checkCurrentRepositoryReadsLegacyFaction(t, config, id)
}

// privateSchema returns a connection and its configuration on a new, empty schema.
func privateSchema(t *testing.T) (*sql.DB, *pgx.ConnConfig) {
	t.Helper()
	config := testDatabaseConfig(t)
	admin := stdlib.OpenDB(*config)
	t.Cleanup(func() { _ = admin.Close() })
	schema := "schema_" + strings.ReplaceAll(randomUUID(t), "-", "")
	if _, err := admin.ExecContext(context.Background(), `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.ExecContext(context.Background(), `DROP SCHEMA `+schema+` CASCADE`) })
	config.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*config)
	t.Cleanup(func() { _ = db.Close() })
	return db, config
}

// seedLegacyFaction writes a pre-background faction and battle at schema version 8 and
// returns the battle's snapshot.
func seedLegacyFaction(t *testing.T, db *sql.DB, id string) string {
	t.Helper()
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `INSERT INTO factions(id,name,doctrine,ruleset_id,blueprint) VALUES($1,'Legacy','Scout','old','{"units":[]}')`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO battle_runs(id,scenario_id,scenario_digest,scenario_snapshot,requested_ticks,initial_state,current_state) VALUES($1,'old','digest','{"factions":[{"name":"Legacy","doctrine":"Scout"}],"ruleset":{"designBudget":120}}',10,'{}','{}')`, id); err != nil {
		t.Fatal(err)
	}
	return battleSnapshot(t, db, id)
}

func battleSnapshot(t *testing.T, db *sql.DB, id string) string {
	t.Helper()
	var value string
	if err := db.QueryRowContext(context.Background(), `SELECT scenario_snapshot::text FROM battle_runs WHERE id=$1`, id).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

// checkCurrentRepositoryReadsLegacyFaction reads, edits and lists the migrated profile
// through today's faction repository, and creates a new one beside it.
func checkCurrentRepositoryReadsLegacyFaction(t *testing.T, config *pgx.ConnConfig, id string) {
	t.Helper()
	ctx := context.Background()
	poolConfig, err := pgxpool.ParseConfig(config.ConnString())
	if err != nil {
		t.Fatal(err)
	}
	poolConfig.ConnConfig.RuntimeParams["search_path"] = config.RuntimeParams["search_path"]
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repo := factionrepo.NewPostgres(pool)
	legacy, err := repo.GetFaction(ctx, id)
	if err != nil || legacy.Name != "Legacy" || legacy.Doctrine != "Scout" || legacy.Background != "" || legacy.RulesetID != "old" {
		t.Fatal(legacy, err)
	}
	legacy.Background = "Written later"
	if err = repo.UpdateFaction(ctx, *legacy); err != nil {
		t.Fatal(err)
	}
	listed, err := repo.ListFactions(ctx)
	if err != nil || len(listed) != 1 || listed[0].Background != "Written later" {
		t.Fatal(listed, err)
	}
	if fields := encodedFields(legacy); fields["blueprint"] != nil {
		t.Fatal("legacy blueprint leaked")
	}
	if err = repo.CreateFaction(ctx, faction.Faction{ID: randomUUID(t), Name: "New", Doctrine: "Defend", Background: "New origins", OwnerID: "owner"}, 0); err != nil {
		t.Fatal(err)
	}
}

func encodedFields(value any) map[string]any {
	encoded, _ := json.Marshal(value)
	var fields map[string]any
	_ = json.Unmarshal(encoded, &fields)
	return fields
}
