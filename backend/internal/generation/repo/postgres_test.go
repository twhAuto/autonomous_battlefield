// Checks concurrent admission, per-account isolation, idempotency, quotas, expiry, and
// generation accounting.

package repo

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/generation"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/postgres"
)

func database(t *testing.T) *Postgres {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("generation_test_%d", time.Now().UnixNano())
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); admin.Close() })
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	{
		_, err = postgres.Migrate(ctx, u.String(), 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
	}
	db, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return NewPostgres(db)
}

// writer is the model a test job is admitted with.
var writer = generation.Admission{ProfileID: "codex-subscription-local", ModelLabel: "Writer", Runtime: "cli"}

var draft = generation.Input{Name: " Northwind ", Doctrine: " Defend the weak. ", Background: " Old roots. "}

func admit(t *testing.T, p *Postgres, actor, key string, c generation.Config) generation.Job {
	t.Helper()
	j, _, err := p.Admit(context.Background(), actor, key, draft, c, writer)
	if err != nil {
		t.Fatal(err)
	}
	return j
}
func sqlExec(t *testing.T, p *Postgres, query string, args ...any) {
	t.Helper()
	{
		_, err := p.db.Exec(context.Background(), query, args...)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestAdmissionConcurrencyIdempotencyAndIsolation(t *testing.T) {
	p := database(t)
	ctx := context.Background()
	c := generation.DefaultConfig()
	if accepted := admitConcurrently(t, p, c, 12); accepted != 1 {
		t.Fatalf("admitted %d", accepted)
	}
	o, err := p.Options(ctx, "one", c)
	if err != nil || o.Remaining != 9 || o.ActiveJobID == "" || o.ResetAt.UTC().Hour() != 0 {
		t.Fatalf("options %+v %v", o, err)
	}
	j := admit(t, p, "two", "same", c)
	again := admit(t, p, "two", "same", c)
	if j.ID != again.ID || j.Input.Name != "Northwind" {
		t.Fatal("idempotency/normalization")
	}
	changed := draft
	changed.Doctrine = "Attack"
	if _, _, err = p.Admit(ctx, "two", "same", changed, c, writer); !errors.Is(err, generation.ErrConflict) {
		t.Fatal(err)
	}
	if _, err = p.Get(ctx, "one", j.ID); !errors.Is(err, generation.ErrNotFound) {
		t.Fatal("cross-account read", err)
	}
	for _, request := range []struct {
		key   string
		input generation.Input
	}{{"bad", generation.Input{}}, {"", draft}, {strings.Repeat("k", 201), draft}} {
		if _, _, err = p.Admit(ctx, "two", request.key, request.input, c, writer); !errors.Is(err, generation.ErrInvalid) {
			t.Fatal(err)
		}
	}
}

// admitConcurrently sends n simultaneous admissions for one account with distinct keys
// and returns how many were accepted; every rejection must be ErrLimited.
func admitConcurrently(t *testing.T, p *Postgres, c generation.Config, n int) int {
	t.Helper()
	var wg sync.WaitGroup
	results := make(chan error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, e := p.Admit(context.Background(), "one", fmt.Sprint(i), draft, c, writer)
			results <- e
		}()
	}
	wg.Wait()
	close(results)
	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
		} else if !errors.Is(err, generation.ErrLimited) {
			t.Fatal(err)
		}
	}
	return accepted
}

func TestLifecycleExpiryAndExecutionAccounting(t *testing.T) {
	p := database(t)
	c := generation.DefaultConfig()
	checkQueuedExpiryKeepsCooldown(t, p, c)
	sqlExec(t, p, "UPDATE faction_generation_accounts SET last_admitted_at=now()-interval '31 seconds'")
	checkRunningTimeoutCountsAndIsNotRetried(t, p, c)
}

// checkQueuedExpiryKeepsCooldown lets a queued job expire unclaimed: it does not use
// the daily allowance, but the admission cooldown still applies.
func checkQueuedExpiryKeepsCooldown(t *testing.T, p *Postgres, c generation.Config) {
	t.Helper()
	ctx := context.Background()
	j := admit(t, p, "one", "queued", c)
	if _, err := p.Claim(ctx, "worker", []string{"wrong-profile"}); !errors.Is(err, generation.ErrNotFound) {
		t.Fatal(err)
	}
	sqlExec(t, p, "UPDATE faction_background_generations SET expires_at=now()-interval '1 second' WHERE id=$1", j.ID)
	expired, err := p.Get(ctx, "one", j.ID)
	if err != nil || expired.Status != "expired" || expired.ErrorCategory != "queue_timeout" {
		t.Fatalf("%+v %v", expired, err)
	}
	o, err := p.Options(ctx, "one", c)
	if err != nil || o.Remaining != 9 || o.ActiveJobID != "" {
		t.Fatalf("expiry accounting %+v %v", o, err)
	}
	if _, _, err = p.Admit(ctx, "one", "cooldown", draft, c, writer); !errors.Is(err, generation.ErrLimited) {
		t.Fatal("expiry must retain cooldown", err)
	}
}

// checkRunningTimeoutCountsAndIsNotRetried claims a job once, lets it time out while
// running, and requires the late submission to fail, the run to count against the
// allowance, and the job not to be claimed again.
func checkRunningTimeoutCountsAndIsNotRetried(t *testing.T, p *Postgres, c generation.Config) {
	t.Helper()
	ctx := context.Background()
	j := admit(t, p, "one", "running", c)
	running, err := p.Claim(ctx, "worker", []string{c.ProfileID})
	if err != nil || running.ID != j.ID || running.StartedAt == nil || running.Status != "running" {
		t.Fatalf("claim %+v %v", running, err)
	}
	if _, err = p.Claim(ctx, "other", []string{c.ProfileID}); !errors.Is(err, generation.ErrNotFound) {
		t.Fatal("double claim", err)
	}
	if v, err := p.Context(ctx, j.ID); err != nil || v.Input.Doctrine != "Defend the weak." {
		t.Fatal(v, err)
	}
	sqlExec(t, p, "UPDATE faction_background_generations SET expires_at=now()-interval '1 second' WHERE id=$1", j.ID)
	if ok, err := p.Submit(ctx, j.ID, "late"); err != nil || ok {
		t.Fatal("late submit", ok, err)
	}
	expired, err := p.Get(ctx, "one", j.ID)
	if err != nil || expired.Status != "failed" || expired.ErrorCategory != "timeout" {
		t.Fatal(expired, err)
	}
	if _, err = p.Context(ctx, j.ID); !errors.Is(err, generation.ErrNotFound) {
		t.Fatal(err)
	}
	o, err := p.Options(ctx, "one", c)
	if err != nil || o.Remaining != 8 {
		t.Fatal("started failure must count", o, err)
	}
	if _, err = p.Claim(ctx, "retry", []string{c.ProfileID}); !errors.Is(err, generation.ErrNotFound) {
		t.Fatal("running timeout retried", err)
	}
}

func TestSubmissionLimitsDailyResetAndMetadata(t *testing.T) {
	p := database(t)
	ctx := context.Background()
	c := generation.DefaultConfig()
	c.DailyAllowance = 2
	consumeGenerationAllowance(t, p, ctx, c)
	o, err := p.Options(ctx, "one", c)
	if err != nil || o.Remaining != 0 || !o.RetryAt.Equal(o.ResetAt) {
		t.Fatal(o, err)
	}
	{
		_, _, err = p.Admit(ctx, "one", "exhausted", draft, c, writer)
		if !errors.Is(err, generation.ErrLimited) {
			t.Fatal(err)
		}
	}
	sqlExec(t, p, "UPDATE faction_background_generations SET created_at=date_trunc('day',now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'-interval '1 second'")
	sqlExec(t, p, "UPDATE faction_generation_accounts SET last_admitted_at=now()-interval '31 seconds'")
	o, err = p.Options(ctx, "one", c)
	if err != nil || o.Remaining != 2 {
		t.Fatal(o, err)
	}
	j := admit(t, p, "one", "newday", c)
	{
		_, err = p.Claim(ctx, "worker", []string{c.ProfileID})
		if err != nil {
			t.Fatal(err)
		}
	}
	{
		ok, err := p.Finish(ctx, j.ID, "", ai.Usage{}, nil)
		if !ok || err != nil {
			t.Fatal(ok, err)
		}
	}
	v, err := p.Get(ctx, "one", j.ID)
	if err != nil || v.Status != "failed" || v.ErrorCategory != "no_submission" {
		t.Fatal(v, err)
	}
}
func consumeGenerationAllowance(t *testing.T, p *Postgres, ctx context.Context, c generation.Config) {
	for i := range 2 {
		sqlExec(t, p, "UPDATE faction_generation_accounts SET last_admitted_at=now()-interval '31 seconds'")
		j := admit(t, p, "one", fmt.Sprint(i), c)
		{
			_, err := p.Claim(ctx, "worker", []string{c.ProfileID})
			if err != nil {
				t.Fatal(err)
			}
		}
		if i == 0 {
			checkInvalidSubmissionLimit(t, p, ctx, j)
			if _, err := p.Finish(ctx, j.ID, "invalid_output", ai.Usage{}, nil); err != nil {
				t.Fatal(err)
			}
		} else {
			checkSuccessfulSubmission(t, p, ctx, j)
		}
	}

}

func checkInvalidSubmissionLimit(t *testing.T, p *Postgres, ctx context.Context, j generation.Job) {
	for _, value := range []string{"", strings.Repeat("界", 1001), "  "} {
		{
			ok, err := p.Submit(ctx, j.ID, value)
			if ok || !errors.Is(err, generation.ErrInvalid) {
				t.Fatal(ok, err)
			}
		}
	}
	{
		ok, err := p.Submit(ctx, j.ID, "Too late")
		if ok || err != nil {
			t.Fatal(ok, err)
		}
	}
	v, err := p.Get(ctx, "one", j.ID)
	if err != nil || v.ErrorCategory != "invalid_output" {
		t.Fatal(v, err)
	}
}

func checkSuccessfulSubmission(t *testing.T, p *Postgres, ctx context.Context, j generation.Job) {
	t.Helper()
	value := strings.Repeat("界", 1000)
	for range 2 {
		{
			ok, err := p.Submit(ctx, j.ID, " "+value+" ")
			if err != nil || !ok {
				t.Fatal(ok, err)
			}
		}
	}
	{
		ok, err := p.Submit(ctx, j.ID, "Changed")
		if err != nil || ok {
			t.Fatal("result changed", ok, err)
		}
	}
	for range 2 {
		{
			ok, err := p.Finish(ctx, j.ID, "", ai.Usage{}, map[string]any{"modelIdentifier": "actual-model"})
			if err != nil || !ok {
				t.Fatal(ok, err)
			}
		}
	}
	v, err := p.Get(ctx, "one", j.ID)
	if err != nil || v.Background != value || v.Status != "succeeded" || v.ModelLabel != "actual-model" {
		t.Fatal(v, err)
	}
}
