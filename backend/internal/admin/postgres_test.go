package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/profile"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	"github.com/twh09/autonomous_battlefield/backend/internal/postgres"
)

func isolatedAdminDatabase(t *testing.T) string {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	schema := fmt.Sprintf("admin_test_%d", time.Now().UnixNano())
	if _, err = db.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE") })
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
	return u.String()
}
func TestDurableModelAccessAcrossAPIInstances(t *testing.T) {
	connection := isolatedAdminDatabase(t)
	ctx := context.Background()
	first, err := pgxpool.New(ctx, connection)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := pgxpool.New(ctx, connection)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	h := &Handler{Service: &Service{Repository: &Postgres{DB: first}, Profiles: registry()}}
	other := &Handler{Service: &Service{Repository: &Postgres{DB: second}, Profiles: registry()}}
	w := request(h, "GET", nil, authn.Admin)
	var settings Settings
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &settings) != nil {
		t.Fatalf("initial settings: %d %s", w.Code, w.Body.String())
	}
	settings.Policy.Providers["openai"] = false
	if w = request(h, "PUT", settings, authn.Admin); w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	if _, err = other.Service.Profiles.WithAccessPolicy(other.Service.ReadPolicy).Require("api"); err == nil {
		t.Fatal("second replica ignored persisted disable")
	}
	if w = request(other, "PUT", settings, authn.Admin); w.Code != 409 {
		t.Fatalf("stale write=%d", w.Code)
	}
	settings, err = other.Service.Repository.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	settings.Policy.Providers["openai"] = true
	if w = request(other, "PUT", settings, authn.Admin); w.Code != 200 {
		t.Fatalf("enable=%d %s", w.Code, w.Body.String())
	}
	if _, err = h.Service.Profiles.WithAccessPolicy(h.Service.ReadPolicy).Require("api"); err != nil {
		t.Fatal("first replica did not see re-enable", err)
	}
	var updatedBy string
	if err = first.QueryRow(ctx, "SELECT updated_by FROM model_access_policy WHERE id").Scan(&updatedBy); err != nil || updatedBy != "admin" {
		t.Fatalf("updated_by=%q err=%v", updatedBy, err)
	}
}

// Saved policies keep the legacy tester spelling that images before the rename compare
// against, while the API reads and writes tester.
func TestPoliciesStoreTheLegacyTesterSpelling(t *testing.T) {
	ctx := context.Background()
	db, err := pgxpool.New(ctx, isolatedAdminDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := &Postgres{DB: db}
	settings, err := store.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	settings.Policy = profile.AccessPolicy{Providers: map[string]bool{}, Profiles: map[string]profile.Access{
		"cli": {Enabled: true, AllowedRoles: []string{"admin", "tester"}},
		"api": {AllowedRoles: []string{}},
	}}
	if _, err = store.Save(ctx, settings, "admin"); err != nil {
		t.Fatal(err)
	}
	if got := settings.Policy.Profiles["cli"].AllowedRoles; got[1] != "tester" {
		t.Fatalf("save rewrote the caller's policy: %v", got)
	}
	var cli, api string
	if err = db.QueryRow(ctx, "SELECT policy#>>'{profiles,cli,allowedRoles}', policy#>>'{profiles,api,allowedRoles}' FROM model_access_policy WHERE id").Scan(&cli, &api); err != nil || cli != `["admin", "developer"]` || api != "[]" {
		t.Fatalf("stored roles = %s %s, err=%v", cli, api, err)
	}
	read, err := store.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := read.Policy.Profiles["cli"].AllowedRoles; len(got) != 2 || got[0] != "admin" || got[1] != "tester" {
		t.Fatalf("cli roles = %v", got)
	}
	if got := read.Policy.Profiles["api"].AllowedRoles; got == nil || len(got) != 0 {
		t.Fatalf("api roles = %#v", got)
	}
}
