// Checks background-generation schema changes preserve faction profiles and historical
// snapshots.

package generation

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/twh09/autonomous_battlefield/backend/internal/faction"
	factionrepo "github.com/twh09/autonomous_battlefield/backend/internal/faction/repo"
	"github.com/twh09/autonomous_battlefield/backend/migrations"
)

func TestMigrationPreservesProfilesAndHistoricalSnapshots(t *testing.T) {
	p := database(t)
	ctx := context.Background()
	pool := p.DB.(*pgxpool.Pool)
	db := stdlib.OpenDB(*pool.Config().ConnConfig)
	defer db.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Down(ctx); err != nil {
		t.Fatal(err)
	} // Back to the prior faction schema.
	id := "7f22e693-bacd-46bb-b942-64fa613cd388"
	sqlExec(t, p, `INSERT INTO factions(id,name,doctrine,ruleset_id,blueprint) VALUES($1,'Legacy','Scout','old','{"units":[]}')`, id)
	sqlExec(t, p, `INSERT INTO battle_runs(id,scenario_id,scenario_digest,scenario_snapshot,requested_ticks,initial_state,current_state) VALUES($1,'old','digest','{"factions":[{"name":"Legacy","doctrine":"Scout"}],"ruleset":{"designBudget":120}}',10,'{}','{}')`, id)
	var before []byte
	if err = p.DB.QueryRow(ctx, `SELECT scenario_snapshot FROM battle_runs WHERE id=$1`, id).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	repo := factionrepo.NewPostgres(p.DB)
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
	var after []byte
	if err = p.DB.QueryRow(ctx, `SELECT scenario_snapshot FROM battle_runs WHERE id=$1`, id).Scan(&after); err != nil || string(before) != string(after) {
		t.Fatal("historical snapshot rewritten", err)
	}
	encoded, _ := json.Marshal(legacy)
	var fields map[string]any
	_ = json.Unmarshal(encoded, &fields)
	if _, exists := fields["blueprint"]; exists {
		t.Fatal("legacy blueprint leaked")
	}
	if err = repo.CreateFaction(ctx, faction.Faction{ID: "4db8d152-1e04-4d10-8824-859c6ed72878", Name: "New", Doctrine: "Defend", Background: "New origins"}); err != nil {
		t.Fatal(err)
	}
}
