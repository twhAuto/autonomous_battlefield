// Checks concurrent admission, per-account isolation, idempotency, quotas, expiry, and
// generation accounting.

package generation

import (
	"github.com/twh09/autonomous_battlefield/backend/internal/testutil"

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
	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/postgres"
)

func database(t *testing.T) *Postgres {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, raw)
	testutil.Require(t, err == nil, func() { t.Fatal(err) })
	schema := fmt.Sprintf("generation_test_%d", time.Now().UnixNano())
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); admin.Close() })
	u, err := url.Parse(raw)
	testutil.Require(t, err == nil, func() { t.Fatal(err) })
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	{
		_, err = postgres.Migrate(ctx, u.String(), 5*time.Second)
		testutil.Require(t, err == nil, func() { t.Fatal(err) })
	}
	db, err := pgxpool.New(ctx, u.String())
	testutil.Require(t, err == nil, func() { t.Fatal(err) })
	t.Cleanup(db.Close)
	return &Postgres{DB: db}
}

var draft = Input{Name: " Northwind ", Doctrine: " Defend the weak. ", Background: " Old roots. "}

func admit(t *testing.T, p *Postgres, actor, key string, c Config) Job {
	t.Helper()
	j, _, err := p.Admit(context.Background(), actor, key, draft, c, "Writer")
	testutil.Require(t, err == nil, func() { t.Fatal(err) })
	return j
}
func sqlExec(t *testing.T, p *Postgres, query string, args ...any) {
	t.Helper()
	{
		_, err := p.DB.Exec(context.Background(), query, args...)
		testutil.Require(t, err == nil, func() { t.Fatal(err) })
	}
}
func TestAdmissionConcurrencyIdempotencyAndIsolation(t *testing.T) {
	p := database(t)
	ctx := context.Background()
	c := DefaultConfig()
	var wg sync.WaitGroup
	results := make(chan error, 12)
	for i := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, e := p.Admit(ctx, "one", fmt.Sprint(i), draft, c, "Writer")
			results <- e
		}()
	}
	wg.Wait()
	close(results)
	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
		} else {
			testutil.Require(t, errors.Is(err, ErrLimited), func() { t.Fatal(err) })
		}
	}
	testutil.Require(t, accepted == 1, func() { t.Fatalf("admitted %d", accepted) })
	o, err := p.Options(ctx, "one", c)
	testutil.Require(t, !(err != nil || o.Remaining != 9 || o.ActiveJobID == "" || o.ResetAt.UTC().Hour() != 0), func() { t.Fatalf("options %+v %v", o, err) })
	j := admit(t, p, "two", "same", c)
	again := admit(t, p, "two", "same", c)
	testutil.Require(t, !(j.ID != again.ID || j.Input.Name != "Northwind"), func() { t.Fatal("idempotency/normalization") })
	changed := draft
	changed.Doctrine = "Attack"
	{
		_, _, err = p.Admit(ctx, "two", "same", changed, c, "")
		testutil.Require(t, errors.Is(err, ErrConflict), func() { t.Fatal(err) })
	}
	{
		_, err = p.Get(ctx, "one", j.ID)
		testutil.Require(t, errors.Is(err, ErrNotFound), func() { t.Fatal("cross-account read", err) })
	}
	{
		_, _, err = p.Admit(ctx, "two", "bad", Input{}, c, "")
		testutil.Require(t, errors.Is(err, ErrInvalid), func() { t.Fatal(err) })
	}
	{
		_, _, err = p.Admit(ctx, "two", "", draft, c, "")
		testutil.Require(t, errors.Is(err, ErrInvalid), func() { t.Fatal(err) })
	}
	{
		_, _, err = p.Admit(ctx, "two", strings.Repeat("k", 201), draft, c, "")
		testutil.Require(t, errors.Is(err, ErrInvalid), func() { t.Fatal(err) })
	}
}
func TestLifecycleRefundsAndExecutionAccounting(t *testing.T) {
	p := database(t)
	ctx := context.Background()
	c := DefaultConfig()
	j := admit(t, p, "one", "queued", c)
	{
		_, err := p.Claim(ctx, "worker", []string{"wrong-profile"})
		testutil.Require(t, errors.Is(err, ErrNotFound), func() { t.Fatal(err) })
	}
	sqlExec(t, p, "UPDATE faction_background_generations SET expires_at=now()-interval '1 second' WHERE id=$1", j.ID)
	expired, err := p.Get(ctx, "one", j.ID)
	testutil.Require(t, !(err != nil || expired.Status != "expired" || expired.ErrorCategory != "queue_timeout"), func() { t.Fatalf("%+v %v", expired, err) })
	o, err := p.Options(ctx, "one", c)
	testutil.Require(t, !(err != nil || o.Remaining != 10 || o.ActiveJobID != ""), func() { t.Fatalf("refund %+v %v", o, err) })
	{
		_, _, err = p.Admit(ctx, "one", "cooldown", draft, c, "")
		testutil.Require(t, errors.Is(err, ErrLimited), func() { t.Fatal("refund must retain cooldown", err) })
	}
	sqlExec(t, p, "UPDATE faction_generation_accounts SET last_admitted_at=now()-interval '31 seconds'")
	j = admit(t, p, "one", "running", c)
	running, err := p.Claim(ctx, "worker", []string{c.ProfileID})
	testutil.Require(t, !(err != nil || running.ID != j.ID || running.StartedAt == nil || running.Status != "running"), func() { t.Fatalf("claim %+v %v", running, err) })
	{
		_, err = p.Claim(ctx, "other", []string{c.ProfileID})
		testutil.Require(t, errors.Is(err, ErrNotFound), func() { t.Fatal("double claim", err) })
	}
	{
		v, err := p.Context(ctx, j.ID)
		testutil.Require(t, !(err != nil || v.Input.Doctrine != "Defend the weak."), func() { t.Fatal(v, err) })
	}
	sqlExec(t, p, "UPDATE faction_background_generations SET expires_at=now()-interval '1 second' WHERE id=$1", j.ID)
	{
		ok, err := p.Submit(ctx, j.ID, "late")
		testutil.Require(t, !(err != nil || ok), func() { t.Fatal("late submit", ok, err) })
	}
	expired, err = p.Get(ctx, "one", j.ID)
	testutil.Require(t, !(err != nil || expired.Status != "failed" || expired.ErrorCategory != "timeout"), func() { t.Fatal(expired, err) })
	{
		_, err = p.Context(ctx, j.ID)
		testutil.Require(t, errors.Is(err, ErrNotFound), func() { t.Fatal(err) })
	}
	o, err = p.Options(ctx, "one", c)
	testutil.Require(t, !(err != nil || o.Remaining != 9), func() { t.Fatal("started failure must count", o, err) })
	{
		_, err = p.Claim(ctx, "retry", []string{c.ProfileID})
		testutil.Require(t, errors.Is(err, ErrNotFound), func() { t.Fatal("running timeout retried", err) })
	}
}
func TestSubmissionLimitsDailyResetAndMetadata(t *testing.T) {
	p := database(t)
	ctx := context.Background()
	c := DefaultConfig()
	c.DailyAllowance = 2
	consumeGenerationAllowance(t, p, ctx, c)
	o, err := p.Options(ctx, "one", c)
	testutil.Require(t, !(err != nil || o.Remaining != 0 || !o.RetryAt.Equal(o.ResetAt)), func() { t.Fatal(o, err) })
	{
		_, _, err = p.Admit(ctx, "one", "exhausted", draft, c, "")
		testutil.Require(t, errors.Is(err, ErrLimited), func() { t.Fatal(err) })
	}
	sqlExec(t, p, "UPDATE faction_background_generations SET created_at=date_trunc('day',now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'-interval '1 second'")
	sqlExec(t, p, "UPDATE faction_generation_accounts SET last_admitted_at=now()-interval '31 seconds'")
	o, err = p.Options(ctx, "one", c)
	testutil.Require(t, !(err != nil || o.Remaining != 2), func() { t.Fatal(o, err) })
	j := admit(t, p, "one", "newday", c)
	{
		_, err = p.Claim(ctx, "worker", []string{c.ProfileID})
		testutil.Require(t, err == nil, func() { t.Fatal(err) })
	}
	{
		ok, err := p.Finish(ctx, j.ID, "", ai.Usage{}, nil)
		testutil.Require(t, !(!ok || err != nil), func() { t.Fatal(ok, err) })
	}
	v, err := p.Get(ctx, "one", j.ID)
	testutil.Require(t, !(err != nil || v.Status != "failed" || v.ErrorCategory != "no_submission"), func() { t.Fatal(v, err) })
}
func TestInputAndConfiguration(t *testing.T) {
	{
		err := DefaultConfig().Validate()
		testutil.Require(t, err == nil, func() { t.Fatal(err) })
	}
	{
		err := (Config{}).Validate()
		testutil.Require(t, err != nil, func() { t.Fatal("invalid config accepted") })
	}
	for _, input := range []Input{{}, {Name: "a", Doctrine: " "}, {Name: "a", Doctrine: strings.Repeat("😀", 4001)}, {Name: "a", Doctrine: "d", Background: strings.Repeat("😀", 1001)}} {
		{
			_, err := input.Normalized()
			testutil.Require(t, errors.Is(err, ErrInvalid), func() { t.Fatal(err) })
		}
	}
	for _, bg := range []string{"", strings.Repeat("😀", 1000)} {
		got, err := (Input{Name: " a ", Doctrine: " d ", Background: " " + bg + " "}).Normalized()
		testutil.Require(t, !(err != nil || got.Name != "a" || got.Doctrine != "d" || got.Background != bg), func() { t.Fatal(got, err) })
	}
}

func consumeGenerationAllowance(t *testing.T, p *Postgres, ctx context.Context, c Config) {
	for i := range 2 {
		sqlExec(t, p, "UPDATE faction_generation_accounts SET last_admitted_at=now()-interval '31 seconds'")
		j := admit(t, p, "one", fmt.Sprint(i), c)
		{
			_, err := p.Claim(ctx, "worker", []string{c.ProfileID})
			testutil.Require(t, err == nil, func() { t.Fatal(err) })
		}
		if i == 0 {
			checkInvalidSubmissionLimit(t, p, ctx, j)
		} else {
			value := strings.Repeat("界", 1000)
			for range 2 {
				{
					ok, err := p.Submit(ctx, j.ID, " "+value+" ")
					testutil.Require(t, !(err != nil || !ok), func() { t.Fatal(ok, err) })
				}
			}
			{
				ok, err := p.Submit(ctx, j.ID, "Changed")
				testutil.Require(t, !(err != nil || ok), func() { t.Fatal("result changed", ok, err) })
			}
			for range 2 {
				{
					ok, err := p.Finish(ctx, j.ID, "", ai.Usage{}, map[string]any{"modelIdentifier": "actual-model"})
					testutil.Require(t, !(err != nil || !ok), func() { t.Fatal(ok, err) })
				}
			}
			v, err := p.Get(ctx, "one", j.ID)
			testutil.Require(t, !(err != nil || v.Background != value || v.Status != "succeeded" || v.ModelLabel != "actual-model"), func() { t.Fatal(v, err) })
		}
	}

}

func checkInvalidSubmissionLimit(t *testing.T, p *Postgres, ctx context.Context, j Job) {
	for _, value := range []string{"", strings.Repeat("界", 1001), "  "} {
		{
			ok, err := p.Submit(ctx, j.ID, value)
			testutil.Require(t, !(ok || !errors.Is(err, ErrInvalid)), func() { t.Fatal(ok, err) })
		}
	}
	{
		ok, err := p.Submit(ctx, j.ID, "Too late")
		testutil.Require(t, !(ok || err != nil), func() { t.Fatal(ok, err) })
	}
	v, err := p.Get(ctx, "one", j.ID)
	testutil.Require(t, !(err != nil || v.ErrorCategory != "invalid_output"), func() { t.Fatal(v, err) })
}
