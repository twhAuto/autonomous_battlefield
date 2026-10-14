package members

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	"github.com/twh09/autonomous_battlefield/backend/internal/postgres"
)

type groupFunc func(context.Context, []string) error

func (f groupFunc) Replace(ctx context.Context, emails []string) error { return f(ctx, emails) }
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
	schema := fmt.Sprintf("members_%d", time.Now().UnixNano())
	if _, err = root.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = root.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE") })
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
	s := &Store{DB: db, Roles: map[string]authn.Role{"owner@example.com": authn.Admin, "dev@example.com": authn.Developer, "seed@example.com": authn.Player}, Writer: groupFunc(func(context.Context, []string) error { return nil })}
	if err = s.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func member(t *testing.T, s *Store, email string) Member {
	t.Helper()
	ms, _, err := s.List(context.Background())
	must(t, err)
	for _, m := range ms {
		if m.Email == email {
			return m
		}
	}
	t.Fatal("member not found", email)
	return Member{}
}

func TestMembershipGrantRemoveAndReinvite(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if s.Allowed(ctx, "seed@example.com") {
		t.Fatal("import active before sync")
	}
	var sent []string
	s.Writer = groupFunc(func(_ context.Context, emails []string) error { sent = emails; return nil })
	must(t, s.Reconcile(ctx))
	if !s.Allowed(ctx, "seed@example.com") || !reflect.DeepEqual(sent, []string{"seed@example.com"}) {
		t.Fatal("seed not synchronized", sent)
	}
	must(t, s.Change(ctx, "owner", " Friend@Example.com ", "", true))
	m := member(t, s, "friend@example.com")
	must(t, s.Change(ctx, "owner", "friend@example.com", "", true))
	if member(t, s, m.Email).Version != m.Version || s.Allowed(ctx, m.Email) {
		t.Fatal("duplicate grant changed version or activated")
	}
	must(t, s.Reconcile(ctx))
	if !s.Allowed(ctx, m.Email) {
		t.Fatal("grant not active")
	}
	must(t, s.Change(ctx, "owner", "", m.ID, false))
	must(t, s.Change(ctx, "owner", "", m.ID, false))
	if s.Allowed(ctx, m.Email) {
		t.Fatal("removal failed before remote update")
	}
	must(t, s.Reconcile(ctx))
	if !reflect.DeepEqual(sent, []string{"seed@example.com"}) {
		t.Fatal("removed member still in group")
	}
	must(t, s.Change(ctx, "owner", m.Email, "", true))
	if s.Allowed(ctx, m.Email) {
		t.Fatal("reinvite reused old successful version")
	}
	must(t, s.Reconcile(ctx))
	if !s.Allowed(ctx, m.Email) {
		t.Fatal("reinvite failed")
	}
	var grants, removes int
	must(t, s.DB.QueryRow(ctx, "SELECT count(*) FILTER (WHERE action='grant'),count(*) FILTER (WHERE action='remove') FROM player_membership_audit WHERE member_id=$1", m.ID).Scan(&grants, &removes))
	if grants != 2 || removes != 1 {
		t.Fatal("audit not idempotent", grants, removes)
	}
}

func TestMembershipSyncFailurePersistsBackoff(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	calls := 0
	s.Writer = groupFunc(func(context.Context, []string) error {
		calls++
		return errors.New("Cloudflare returned HTTP 429")
	})
	if s.Reconcile(ctx) == nil {
		t.Fatal("expected remote failure")
	}
	_, last, err := s.List(ctx)
	must(t, err)
	if last == "" {
		t.Fatal("missing visible error")
	}
	var attempts int
	var delayed bool
	must(t, s.DB.QueryRow(ctx, "SELECT attempts,next_attempt>now() FROM player_membership_sync WHERE id").Scan(&attempts, &delayed))
	if attempts != 1 || !delayed {
		t.Fatal("no persisted backoff", attempts, delayed)
	}
	must(t, s.Reconcile(ctx))
	if calls != 1 {
		t.Fatal("backoff permitted another remote call", calls)
	}
}

func TestMembershipRestartKeepsRemovalTombstone(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	must(t, s.Change(ctx, "owner", "friend@example.com", "", true))
	m := member(t, s, "friend@example.com")
	must(t, s.Change(ctx, "owner", "", m.ID, false))
	s.Roles[m.Email] = authn.Player
	must(t, s.Initialize(ctx))
	if member(t, s, m.Email).Desired {
		t.Fatal("restart resurrected removal")
	}
}

func TestMembershipDatabaseFailureDeniesAccess(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	must(t, s.Reconcile(ctx))
	if !s.Allowed(ctx, "seed@example.com") {
		t.Fatal("seed not allowed before database failure")
	}
	s.DB.Close()
	if s.Allowed(ctx, "seed@example.com") {
		t.Fatal("database failure allowed access")
	}
}

func TestConcurrentReplicaSyncAndRemoveReinvite(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	must(t, s.Reconcile(ctx))
	must(t, s.Change(ctx, "admin", "race@example.com", "", true))
	m := member(t, s, "race@example.com")
	entered, release := make(chan struct{}), make(chan struct{})
	s.Writer = groupFunc(func(context.Context, []string) error { close(entered); <-release; return nil })
	result := make(chan error, 1)
	go func() { result <- s.Reconcile(ctx) }()
	<-entered
	other := &Store{DB: s.DB, Roles: s.Roles, Writer: groupFunc(func(context.Context, []string) error { t.Error("second replica wrote group concurrently"); return nil })}
	must(t, other.Reconcile(ctx))
	editCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	must(t, other.Change(editCtx, "admin", "", m.ID, false))
	must(t, other.Change(editCtx, "admin", m.Email, "", true))
	close(release)
	if <-result == nil {
		t.Fatal("stale sync transaction committed")
	}
	if s.Allowed(ctx, m.Email) {
		t.Fatal("stale response activated reinvite")
	}
	s.Writer = groupFunc(func(context.Context, []string) error { return nil })
	must(t, s.Reconcile(ctx))
	if !s.Allowed(ctx, m.Email) {
		t.Fatal("failed to recover stale transaction")
	}
}

func TestConcurrentGrantsAndEmptyGroup(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Change(ctx, "owner", "same@example.com", "", true); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	ms, _, err := s.List(ctx)
	must(t, err)
	if len(ms) != 2 {
		t.Fatal("duplicate concurrent members", ms)
	}
	for _, m := range ms {
		must(t, s.Change(ctx, "owner", "", m.ID, false))
	}
	s.Writer = groupFunc(func(_ context.Context, emails []string) error {
		if len(emails) != 0 {
			t.Error(emails)
		}
		return nil
	})
	must(t, s.Reconcile(ctx))
	if s.Allowed(ctx, "same@example.com") {
		t.Fatal("empty group allowed")
	}
}

func TestProtectedAndInvalidMembers(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for _, email := range []string{"bad", "Name <friend@example.com>", "a@example.com\nb@example.com", "a@localhost", ""} {
		if !errors.Is(s.Change(ctx, "owner", email, "", true), ErrInvalid) {
			t.Fatal("invalid accepted", email)
		}
	}
	for _, email := range []string{"Owner@example.com", "dev@example.com"} {
		if !errors.Is(s.Change(ctx, "owner", email, "", true), ErrProtected) {
			t.Fatal("privilege accepted", email)
		}
	}
	if !errors.Is(s.Change(ctx, "owner", "", "bad-id", false), ErrNotFound) {
		t.Fatal("missing removal accepted")
	}
	seed := member(t, s, "seed@example.com")
	s.Roles[seed.Email] = authn.Admin
	if !errors.Is(s.Change(ctx, "owner", "", seed.ID, false), ErrProtected) {
		t.Fatal("promoted identity mutable")
	}
	ms, _, err := s.List(ctx)
	must(t, err)
	if len(ms) != 0 {
		t.Fatal("privileged identity listed")
	}
	s.Writer = groupFunc(func(_ context.Context, emails []string) error {
		if len(emails) > 0 {
			t.Fatal("privileged identity sent", emails)
		}
		return nil
	})
	must(t, s.Reconcile(ctx))
	if s.Allowed(ctx, seed.Email) {
		t.Fatal("privileged identity resolved as player")
	}
}

func TestSyncRecoveryAfterAmbiguousResponse(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	calls := 0
	s.Writer = groupFunc(func(context.Context, []string) error {
		calls++
		return errors.New("Cloudflare request failed; retry pending")
	})
	if s.Reconcile(ctx) == nil || s.Allowed(ctx, "seed@example.com") {
		t.Fatal("ambiguous success activated")
	}
	must(t, s.Initialize(ctx))
	must(t, s.Retry(ctx, "owner"))
	s.Writer = groupFunc(func(context.Context, []string) error { calls++; return nil })
	must(t, s.Reconcile(ctx))
	if calls != 2 || !s.Allowed(ctx, "seed@example.com") {
		t.Fatal("restart did not recover")
	}
	runCtx, cancel := context.WithCancel(ctx)
	cancel()
	s.Run(runCtx)
}

func TestPrivilegePromotionCannotRestoreOldInvitation(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	must(t, s.Reconcile(ctx))
	seed := member(t, s, "seed@example.com")
	// A new replica loads the promoted role, even if the operator missed the
	// documented remove-before-promotion step. Initialization tombstones it.
	promoted := &Store{DB: s.DB, Roles: map[string]authn.Role{seed.Email: authn.Admin}, Writer: s.Writer}
	must(t, promoted.Initialize(ctx))
	must(t, promoted.Initialize(ctx))
	var desired bool
	var audits int
	must(t, s.DB.QueryRow(ctx, "SELECT desired_access FROM player_members WHERE id=$1", seed.ID).Scan(&desired))
	must(t, s.DB.QueryRow(ctx, "SELECT count(*) FROM player_membership_audit WHERE member_id=$1 AND action='remove' AND actor_id='configuration'", seed.ID).Scan(&audits))
	if desired || audits != 1 {
		t.Fatal("promotion did not durably retire Player invitation", desired, audits)
	}
	// Another old replica or later offboarding must never resurrect it.
	must(t, s.Initialize(ctx))
	if s.Allowed(ctx, seed.Email) {
		t.Fatal("role removal restored player")
	}
	promoted.Writer = groupFunc(func(_ context.Context, emails []string) error {
		if len(emails) != 0 {
			t.Error("promoted member in group", emails)
		}
		return nil
	})
	must(t, promoted.Reconcile(ctx))
	offboarded := &Store{DB: s.DB, Roles: map[string]authn.Role{}, Writer: promoted.Writer}
	must(t, offboarded.Initialize(ctx))
	must(t, offboarded.Retry(ctx, "admin"))
	must(t, offboarded.Reconcile(ctx))
	if offboarded.Allowed(ctx, seed.Email) || member(t, offboarded, seed.Email).Desired {
		t.Fatal("offboarding restored invitation")
	}
	must(t, offboarded.Change(ctx, "admin", seed.Email, "", true))
	if offboarded.Allowed(ctx, seed.Email) {
		t.Fatal("explicit reinvite bypassed synchronization")
	}
}

func TestRejectNonConventionalEmailDomains(t *testing.T) {
	for _, email := range []string{"user@[10.0.0.1]", "user@-bad.example", "user@bad-.example", "user@bad..example", "user@" + strings.Repeat("a", 64) + ".com", strings.Repeat("a", 65) + "@example.com"} {
		if _, err := normalize(email); !errors.Is(err, ErrInvalid) {
			t.Fatal("unsupported email accepted", email)
		}
	}
	for _, email := range []string{"friend+game@example.com", "player@sub.example.co.uk"} {
		if got, err := normalize(email); err != nil || got != email {
			t.Fatal(got, err)
		}
	}
}

func TestStartImportsThenReconcilesUntilStopped(t *testing.T) {
	base := testStore(t)
	synced := make(chan []string, 1)
	store, err := NewStore(base.DB, base.Roles, groupFunc(func(_ context.Context, emails []string) error {
		select {
		case synced <- emails:
		default:
		}
		return nil
	}))
	must(t, err)
	must(t, store.Change(context.Background(), "admin", "friend@example.com", "", true))
	stop, err := store.Start(context.Background())
	must(t, err)
	select {
	case emails := <-synced:
		if strings.Join(emails, ",") != "friend@example.com,seed@example.com" {
			t.Fatalf("synced %v", emails)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reconciler never wrote the group")
	}
	stop() // Returns only after the reconciler exits.

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Start(cancelled); err == nil {
		t.Fatal("Start succeeded with a cancelled context")
	}
}
