package members

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/authn"
)

// age moves a member's last change into the past, as if it happened that long ago.
func age(t *testing.T, s *Store, email string, by time.Duration) {
	t.Helper()
	_, err := s.DB.Exec(context.Background(), "UPDATE player_members SET updated_at=now()-make_interval(secs => $2) WHERE email=$1", email, by.Seconds())
	must(t, err)
}

// stored reports whether a row for email exists, including removed members.
func stored(t *testing.T, s *Store, email string) bool {
	t.Helper()
	var exists bool
	must(t, s.DB.QueryRow(context.Background(), "SELECT EXISTS(SELECT 1 FROM player_members WHERE email=$1)", email).Scan(&exists))
	return exists
}

// purge runs Purge and fails the test unless it deleted want members.
func purge(t *testing.T, s *Store, want int64) {
	t.Helper()
	got, err := s.Purge(context.Background())
	must(t, err)
	if got != want {
		t.Fatalf("Purge deleted %d members; want %d", got, want)
	}
}

func TestRemovedMembersExpireAfterConfirmedRetention(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	const email = "friend@example.com"
	must(t, s.Change(ctx, "owner", email, "", true, authn.Tester))
	must(t, s.Reconcile(ctx))
	m := member(t, s, email)
	must(t, s.Change(ctx, "owner", "", m.ID, false, ""))

	// A removal Cloudflare has not confirmed never expires, however old.
	age(t, s, email, removedRetention+time.Hour)
	purge(t, s, 0)
	must(t, s.Reconcile(ctx))
	// A confirmed removal inside the retention period stays listed.
	age(t, s, email, removedRetention-time.Hour)
	purge(t, s, 0)
	// Active members never expire.
	age(t, s, "seed@example.com", removedRetention+time.Hour)

	age(t, s, email, removedRetention+time.Hour)
	purge(t, s, 1)
	if stored(t, s, email) || !stored(t, s, "seed@example.com") {
		t.Fatal("purge deleted the wrong members")
	}
	var linked, unlinked int
	must(t, s.DB.QueryRow(ctx, `SELECT count(*) FILTER (WHERE member_id=$1),
 count(*) FILTER (WHERE member_id IS NULL AND action IN ('grant','remove'))
 FROM player_membership_audit`, m.ID).Scan(&linked, &unlinked))
	if linked != 0 || unlinked != 2 {
		t.Fatalf("audit after purge: %d linked, %d unlinked grant/remove rows", linked, unlinked)
	}

	// A new grant is a fresh invitation: it waits for Cloudflare and starts as a Player.
	must(t, s.Change(ctx, "owner", email, "", true, ""))
	if again := member(t, s, email); again.ID == m.ID || again.Role != authn.Player || allowed(s, ctx, email) {
		t.Fatal("reinvite after purge", again, allowed(s, ctx, email))
	}
	must(t, s.Reconcile(ctx))
	if !allowed(s, ctx, email) {
		t.Fatal("reinvite after purge never became active")
	}
}

func TestPurgeIsSafeAcrossReplicasAndConcurrentGrants(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	const email = "friend@example.com"
	expired := func() {
		t.Helper()
		must(t, s.Change(ctx, "owner", email, "", true, ""))
		must(t, s.Reconcile(ctx))
		must(t, s.Change(ctx, "owner", "", member(t, s, email).ID, false, ""))
		must(t, s.Reconcile(ctx))
		age(t, s, email, removedRetention+time.Hour)
	}

	// Two replicas purging at once delete the member exactly once.
	expired()
	counts := make(chan int64, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			count, err := s.Purge(ctx)
			if err != nil {
				t.Error(err)
			}
			counts <- count
		})
	}
	wg.Wait()
	close(counts)
	var total int64
	for count := range counts {
		total += count
	}
	if total != 1 || stored(t, s, email) {
		t.Fatalf("concurrent purges deleted %d members", total)
	}

	// A grant racing a purge always leaves exactly one desired invitation.
	expired()
	errs := make(chan error, 2)
	wg.Go(func() { _, err := s.Purge(ctx); errs <- err })
	wg.Go(func() { errs <- s.Change(ctx, "owner", email, "", true, "") })
	wg.Wait()
	close(errs)
	for err := range errs {
		must(t, err)
	}
	if !member(t, s, email).Desired {
		t.Fatal("grant lost to a concurrent purge")
	}
}

func TestRunPurgesExpiredMembersOnStart(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	const email = "friend@example.com"
	must(t, s.Change(ctx, "owner", email, "", true, ""))
	must(t, s.Reconcile(ctx))
	must(t, s.Change(ctx, "owner", "", member(t, s, email).ID, false, ""))
	must(t, s.Reconcile(ctx))
	age(t, s, email, removedRetention+time.Hour)

	stop, err := s.Start(ctx)
	must(t, err)
	defer stop()
	deadline := time.Now().Add(5 * time.Second)
	for stored(t, s, email) {
		if time.Now().After(deadline) {
			t.Fatal("reconciler never purged the expired member")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestPurgeLogsCountsAndReasonsWithoutEmails(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	const email = "friend@example.com"
	must(t, s.Change(ctx, "owner", email, "", true, ""))
	must(t, s.Reconcile(ctx))
	must(t, s.Change(ctx, "owner", "", member(t, s, email).ID, false, ""))
	must(t, s.Reconcile(ctx))
	age(t, s, email, removedRetention+time.Hour)

	var out bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&out, nil)))
	defer slog.SetDefault(original)
	s.expire(ctx)
	s.expire(ctx) // Nothing left to purge, so nothing is logged.
	s.DB.Close()
	s.expire(ctx)
	text := out.String()
	for _, want := range []string{`"msg":"player_members_purged","count":1`, `"msg":"player_members_purge_failed","reason":"database_unavailable"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %s in %s", want, text)
		}
	}
	if strings.Count(text, "\n") != 2 || strings.Contains(text, email) {
		t.Fatalf("unexpected purge logs: %s", text)
	}
}
