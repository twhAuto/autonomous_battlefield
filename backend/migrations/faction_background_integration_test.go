// Checks the faction background migration (00009) keeps existing profiles readable and
// editable and leaves historical battle snapshots untouched.

package migrations_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/twh09/autonomous_battlefield/backend/internal/faction"
	factionrepo "github.com/twh09/autonomous_battlefield/backend/internal/faction/repo"
	"github.com/twh09/autonomous_battlefield/backend/internal/postgres"
)

func TestFactionBackgroundMigrationPreservesProfilesAndSnapshots(t *testing.T) {
	config := testDatabaseConfig(t)
	admin := stdlib.OpenDB(*config)
	defer admin.Close()
	ctx := context.Background()
	schema := "background_" + strings.ReplaceAll(randomUUID(t), "-", "")
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
	if _, err = provider.UpTo(ctx, 8); err != nil {
		t.Fatal(err)
	}
	id := randomUUID(t)
	if _, err = db.ExecContext(ctx, `INSERT INTO factions(id,name,doctrine,ruleset_id,blueprint) VALUES($1,'Legacy','Scout','old','{"units":[]}')`, id); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO battle_runs(id,scenario_id,scenario_digest,scenario_snapshot,requested_ticks,initial_state,current_state) VALUES($1,'old','digest','{"factions":[{"name":"Legacy","doctrine":"Scout"}],"ruleset":{"designBudget":120}}',10,'{}','{}')`, id); err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		var value string
		if err := db.QueryRowContext(ctx, `SELECT scenario_snapshot::text FROM battle_runs WHERE id=$1`, id).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := snapshot()

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
	if after := snapshot(); after != before {
		t.Fatalf("historical snapshot rewritten:\n%s\n%s", before, after)
	}

	// The current repository reads and edits the migrated profile.
	poolConfig, err := pgxpool.ParseConfig(config.ConnString())
	if err != nil {
		t.Fatal(err)
	}
	poolConfig.ConnConfig.RuntimeParams["search_path"] = schema
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
	encoded, _ := json.Marshal(legacy)
	var fields map[string]any
	_ = json.Unmarshal(encoded, &fields)
	if _, exists := fields["blueprint"]; exists {
		t.Fatal("legacy blueprint leaked")
	}
	if err = repo.CreateFaction(ctx, faction.Faction{ID: randomUUID(t), Name: "New", Doctrine: "Defend", Background: "New origins", OwnerID: "owner"}); err != nil {
		t.Fatal(err)
	}
}
