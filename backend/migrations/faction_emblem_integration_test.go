// Verifies legacy defaults, persisted choices, legacy edits, and database validation.
package migrations_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/faction"
	factionrepo "github.com/twhAuto/autonomous_battlefield/backend/internal/faction/repo"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/postgres"
)

func TestFactionEmblemMigrationAndPersistence(t *testing.T) {
	ctx := context.Background()
	db, config := privateSchema(t)
	provider, err := postgres.NewMigrationProvider(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 20); err != nil {
		t.Fatal(err)
	}
	id := randomUUID(t)
	if _, err = db.ExecContext(ctx, `INSERT INTO factions(id,name,doctrine) VALUES($1,'Legacy','Scout')`, id); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	repo := newEmblemTestRepository(t, config)
	checkEmblemPersistence(t, repo, id)
	if _, err := db.ExecContext(ctx, `UPDATE factions SET emblem='unknown' WHERE id=$1`, id); err == nil {
		t.Fatal("invalid emblem accepted")
	}
}

func newEmblemTestRepository(t *testing.T, config *pgx.ConnConfig) *factionrepo.Postgres {
	t.Helper()
	poolConfig, err := pgxpool.ParseConfig(config.ConnString())
	if err != nil {
		t.Fatal(err)
	}
	poolConfig.ConnConfig.RuntimeParams["search_path"] = config.RuntimeParams["search_path"]
	pool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return factionrepo.NewPostgres(pool)
}

func checkEmblemPersistence(t *testing.T, repo *factionrepo.Postgres, id string) {
	t.Helper()
	ctx := context.Background()
	legacy, err := repo.GetFaction(ctx, id)
	if err != nil || legacy.Emblem != "star" {
		t.Fatalf("legacy: %#v %v", legacy, err)
	}
	legacy.Emblem = "wolf"
	if err := repo.UpdateFaction(ctx, *legacy); err != nil {
		t.Fatal(err)
	}
	legacy.Emblem = ""
	legacy.Name = "Renamed"
	if err := repo.UpdateFaction(ctx, *legacy); err != nil {
		t.Fatal(err)
	}
	saved, err := repo.GetFaction(ctx, id)
	if err != nil || saved.Emblem != "wolf" || saved.Name != "Renamed" {
		t.Fatalf("legacy edit: %#v %v", saved, err)
	}
	next := faction.Faction{ID: randomUUID(t), Name: "Moonwatch", Doctrine: "Defend", Emblem: "moon", OwnerID: "owner"}
	if err := repo.CreateFaction(ctx, next, 0); err != nil {
		t.Fatal(err)
	}
	listed, err := repo.ListFactions(ctx)
	if err != nil || len(listed) != 2 || listed[0].Emblem != "wolf" || listed[1].Emblem != "moon" {
		t.Fatalf("list: %#v %v", listed, err)
	}
}
