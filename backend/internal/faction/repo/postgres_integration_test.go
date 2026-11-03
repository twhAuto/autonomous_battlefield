// Exercises the Player faction limit against PostgreSQL: retries, deletion, owners who
// already hold several factions, and concurrent creates by one owner.

package repo

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	"github.com/twh09/autonomous_battlefield/backend/internal/faction"
	factionservice "github.com/twh09/autonomous_battlefield/backend/internal/faction/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/postgres"
)

func TestPlayerFactionLimitAgainstPostgres(t *testing.T) {
	ctx, db := integrationDatabase(t)
	owner := "player-" + randomUUID(t)
	t.Cleanup(func() { _, _ = db.Exec(context.Background(), `DELETE FROM factions WHERE owner_id = $1`, owner) })
	player := authn.WithActor(ctx, authn.Actor{ID: owner, Role: authn.Player})
	profiles := factionservice.NewService(NewPostgres(db), nil)
	first, second := profile(t, "First"), profile(t, "Second")

	if err := profiles.CreateFaction(player, first); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if err := profiles.CreateFaction(player, first); err != nil {
		t.Fatalf("identical retry: %v", err)
	}
	if err := profiles.CreateFaction(player, second); !errors.Is(err, factionservice.ErrFactionLimitReached) {
		t.Fatalf("second create error = %v, want ErrFactionLimitReached", err)
	}
	if err := profiles.DeleteFaction(player, first.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := profiles.CreateFaction(player, second); err != nil {
		t.Fatalf("create after delete: %v", err)
	}
	if got := ownedFactions(t, db, owner); got != 1 {
		t.Fatalf("owned factions = %d, want 1", got)
	}
}

func TestPlayerWithSeveralFactionsKeepsThemAgainstPostgres(t *testing.T) {
	ctx, db := integrationDatabase(t)
	owner := "player-" + randomUUID(t)
	t.Cleanup(func() { _, _ = db.Exec(context.Background(), `DELETE FROM factions WHERE owner_id = $1`, owner) })
	repo := NewPostgres(db)
	// Unlimited creates stand in for factions made while the owner was a Tester.
	held := []faction.Faction{stored(t, owner, "Held A"), stored(t, owner, "Held B")}
	for _, f := range held {
		if err := repo.CreateFaction(ctx, f, 0); err != nil {
			t.Fatal(err)
		}
	}
	next := stored(t, owner, "Next")
	for _, f := range held {
		if err := repo.CreateFaction(ctx, next, 1); !errors.Is(err, factionservice.ErrFactionLimitReached) {
			t.Fatalf("create while owning factions error = %v, want ErrFactionLimitReached", err)
		}
		if err := repo.DeleteFaction(ctx, f.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.CreateFaction(ctx, next, 1); err != nil {
		t.Fatalf("create after deleting every faction: %v", err)
	}
}

// TestConcurrentPlayerCreatesAgainstPostgres holds the owner's lock until two creates
// both wait on it, so they overlap; exactly one may then succeed.
func TestConcurrentPlayerCreatesAgainstPostgres(t *testing.T) {
	ctx, db := integrationDatabase(t)
	owner := "player-" + randomUUID(t)
	t.Cleanup(func() { _, _ = db.Exec(context.Background(), `DELETE FROM factions WHERE owner_id = $1`, owner) })
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	holder, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback(context.Background()) }()
	if _, err := holder.Exec(ctx, `SELECT pg_advisory_xact_lock($1, hashtext($2))`, factionOwnerLock, owner); err != nil {
		t.Fatal(err)
	}

	repo := NewPostgres(db)
	creates := []faction.Faction{stored(t, owner, "Left"), stored(t, owner, "Right")}
	results := make(chan error, len(creates))
	for _, f := range creates {
		go func() { results <- repo.CreateFaction(ctx, f, 1) }()
	}
	waitForOwnerLockWaiters(t, ctx, db, owner, len(creates))
	if err := holder.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	admitted, refused := 0, 0
	for range creates {
		switch err := <-results; {
		case err == nil:
			admitted++
		case errors.Is(err, factionservice.ErrFactionLimitReached):
			refused++
		default:
			t.Fatalf("concurrent create: %v", err)
		}
	}
	if admitted != 1 || refused != 1 || ownedFactions(t, db, owner) != 1 {
		t.Fatalf("admitted = %d, refused = %d, owned = %d; want exactly one faction", admitted, refused, ownedFactions(t, db, owner))
	}
}

func waitForOwnerLockWaiters(t *testing.T, ctx context.Context, db *pgxpool.Pool, owner string, want int) {
	t.Helper()
	for {
		var waiting int
		if err := db.QueryRow(ctx, `
            SELECT count(*) FROM pg_locks
            WHERE locktype = 'advisory' AND NOT granted AND objsubid = 2
              AND classid = $1::int::oid AND objid = hashtext($2)::oid
        `, factionOwnerLock, owner).Scan(&waiting); err != nil {
			t.Fatalf("count lock waiters: %v", err)
		}
		if waiting == want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("%d creates waiting on the owner lock, want %d", waiting, want)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func ownedFactions(t *testing.T, db *pgxpool.Pool, owner string) int {
	t.Helper()
	var count int
	if err := db.QueryRow(context.Background(), `SELECT count(*) FROM factions WHERE owner_id = $1`, owner).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func profile(t *testing.T, name string) factionservice.ProfileInput {
	t.Helper()
	return factionservice.ProfileInput{ID: randomUUID(t), Name: name, Doctrine: "Scout"}
}

func stored(t *testing.T, owner, name string) faction.Faction {
	t.Helper()
	return faction.Faction{ID: randomUUID(t), Name: name, Doctrine: "Scout", OwnerID: owner}
}

func integrationDatabase(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	if _, err := postgres.Migrate(ctx, url, 5*time.Second); err != nil {
		t.Fatalf("postgres.Migrate() returned an error: %v", err)
	}
	// The concurrency test holds one connection and waits on two more while polling.
	db, err := postgres.Connect(ctx, url, postgres.Options{MaxConnections: 6, MinConnections: 0, MaxConnectionLifetime: time.Minute})
	if err != nil {
		t.Fatalf("postgres.Connect() returned an error: %v", err)
	}
	t.Cleanup(db.Close)
	return ctx, db
}

func randomUUID(t *testing.T) string {
	t.Helper()
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		t.Fatalf("rand.Read() returned an error: %v", err)
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", bytes[0:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:16])
}
