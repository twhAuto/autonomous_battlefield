package people

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	"github.com/twh09/autonomous_battlefield/backend/internal/postgres"
)

// testStore migrates a private schema and returns a store on it.
func testStore(t *testing.T) *Store {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	root, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(root.Close)
	schema := fmt.Sprintf("people_%d", time.Now().UnixNano())
	if _, err = root.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = root.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	if _, err = postgres.Migrate(ctx, u.String(), 5*time.Second); err != nil {
		t.Fatal(err)
	}
	db, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return NewStore(db)
}

// succeeds returns a function that fails the test on an error and passes the value on.
func succeeds[T any](t *testing.T) func(T, error) T {
	return func(value T, err error) T {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
}

var (
	ada   = authn.Actor{ID: "issuer|ada", Role: authn.Player}
	grace = authn.Actor{ID: "issuer|grace", Role: authn.Tester}
	boss  = authn.Actor{ID: "issuer|boss", Role: authn.Admin}
)

func TestRecordStoresRolesAndReturnsTheName(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	text := succeeds[string](t)
	if name := text(store.Record(ctx, ada)); name != "" {
		t.Fatalf("new person name = %q", name)
	}
	text(store.SetName(ctx, ada, "Ada"))
	if name := text(store.Record(ctx, ada)); name != "Ada" {
		t.Fatalf("recorded name = %q", name)
	}
	promoted := ada
	promoted.Role = authn.Tester
	if name := text(store.Record(ctx, promoted)); name != "Ada" {
		t.Fatalf("name after a role change = %q", name)
	}
	if role := storedRole(t, store, ada.ID); role != "tester" {
		t.Fatalf("role = %q, want tester", role)
	}
	for _, ignored := range []authn.Actor{{ID: "issuer|guest", Role: authn.Guest}, {ID: "issuer|service:ci", Role: authn.CI}, {Role: authn.Player}} {
		if name := text(store.Record(ctx, ignored)); name != "" || storedRole(t, store, ignored.ID) != "" {
			t.Fatalf("%+v was recorded", ignored)
		}
	}
}

func TestSetNameValidatesAndKeepsNamesUnique(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	text := succeeds[string](t)
	if name := text(store.SetName(ctx, ada, "  Iron Duke  ")); name != "Iron Duke" {
		t.Fatalf("stored name = %q", name)
	}
	if _, err := store.SetName(ctx, grace, "iron DUKE"); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("case-insensitive duplicate: %v", err)
	}
	if name := text(store.SetName(ctx, ada, "IRON DUKE")); name != "IRON DUKE" {
		t.Fatalf("own name in another case = %q", name)
	}
	for _, invalid := range []string{"", "Al", "Ada!", "Twenty-five characters ok", "Ada\tLovelace"} {
		if _, err := store.SetName(ctx, grace, invalid); !errors.Is(err, ErrInvalidName) {
			t.Fatalf("%q: %v", invalid, err)
		}
	}
	if _, err := store.SetName(ctx, authn.Actor{ID: "issuer|guest", Role: authn.Guest}, "Watcher"); !errors.Is(err, authn.ErrForbidden) {
		t.Fatalf("guest name: %v", err)
	}
	text(store.SetName(ctx, ada, "Duke"))
	if name := text(store.SetName(ctx, grace, "Iron Duke")); name != "Iron Duke" {
		t.Fatalf("released name = %q", name)
	}
}

func TestLabelsNameEveryOwnerWithoutEmails(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	text := succeeds[string](t)
	labelled := succeeds[map[string]string](t)
	text(store.SetName(ctx, ada, "Ada"))
	text(store.Record(ctx, grace))
	text(store.SetName(ctx, boss, "The Boss"))
	labels := labelled(store.Labels(ctx, []string{ada.ID, grace.ID, boss.ID, authn.LegacyOwner, "issuer|unknown", ada.ID, ""}))
	want := map[string]string{ada.ID: "Ada", grace.ID: UnnamedLabel, boss.ID: AdminLabel, authn.LegacyOwner: AdminLabel, "issuer|unknown": UnnamedLabel}
	if !reflect.DeepEqual(labels, want) {
		t.Fatalf("labels = %v, want %v", labels, want)
	}
	if empty := labelled(store.Labels(ctx, nil)); len(empty) != 0 {
		t.Fatalf("labels for no owners = %v", empty)
	}
}

func TestStoreReportsDatabaseFailures(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	store.DB.(*pgxpool.Pool).Close()
	if _, err := store.Record(ctx, ada); err == nil {
		t.Fatal("Record succeeded on a closed pool")
	}
	if _, err := store.SessionName(ctx, ada); err == nil {
		t.Fatal("SessionName succeeded on a closed pool")
	}
	if _, err := store.SetName(ctx, ada, "Ada"); err == nil || errors.Is(err, ErrNameTaken) {
		t.Fatalf("SetName on a closed pool: %v", err)
	}
	if _, err := store.Labels(ctx, []string{ada.ID}); err == nil {
		t.Fatal("Labels succeeded on a closed pool")
	}
}

func storedRole(t *testing.T, store *Store, actorID string) string {
	t.Helper()
	var role string
	err := store.DB.QueryRow(context.Background(), `SELECT COALESCE(max(role), '') FROM people WHERE actor_id = $1`, actorID).Scan(&role)
	if err != nil {
		t.Fatal(err)
	}
	return role
}

// stalledDB blocks every query until its context ends, like an exhausted pool.
type stalledDB struct{ postgres.DBTX }

func (stalledDB) Query(ctx context.Context, _ string, _ ...any) (pgx.Rows, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestLabelsGiveUpOnAStalledDatabase(t *testing.T) {
	started := time.Now()
	_, err := NewStore(stalledDB{}).Labels(context.Background(), []string{"issuer|ada"})
	if elapsed := time.Since(started); !errors.Is(err, context.DeadlineExceeded) || elapsed > 3*labelTimeout {
		t.Fatalf("err = %v after %v", err, elapsed)
	}
}
