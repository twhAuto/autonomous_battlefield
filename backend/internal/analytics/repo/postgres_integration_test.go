// Exercises the analytics battle read against PostgreSQL in a private schema: filters,
// result and controller decoding, and decision totals from the final attempt only.

package repo

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twh09/autonomous_battlefield/backend/internal/analytics"
	analyticsservice "github.com/twh09/autonomous_battlefield/backend/internal/analytics/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/postgres"
)

const (
	recentBattle = "00000000-0000-4000-8000-000000000001"
	olderBattle  = "00000000-0000-4000-8000-000000000002"
	runningOne   = "00000000-0000-4000-8000-000000000003"
	factionA     = "00000000-0000-4000-8000-0000000000aa"
	factionB     = "00000000-0000-4000-8000-0000000000bb"
	leagueID     = "00000000-0000-4000-8000-0000000000cc"
)

func privateDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	schema := fmt.Sprintf("analytics_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal("test database URL is not a URL")
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	if _, err := postgres.Migrate(ctx, parsed.String(), 10*time.Second); err != nil {
		t.Fatal(err)
	}
	db, err := postgres.Connect(ctx, parsed.String(), postgres.Options{MaxConnections: 2, MaxConnectionLifetime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return db
}

func exec(t *testing.T, db *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := db.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func seed(t *testing.T, db *pgxpool.Pool) {
	t.Helper()
	snapshot := func(rules string) string {
		return fmt.Sprintf(`{"rulesVersion":%q,"factions":[{"id":%q,"name":"Amber","controller":{"type":"model","profileId":"claude","modelIdentifier":"sonnet","reasoningEffort":"low"}},{"id":%q,"name":"Basalt","controller":{"type":"scripted"}}]}`, rules, factionA, factionB)
	}
	result := fmt.Sprintf(`{"winnerFactionId":%q,"reason":"castle_destroyed","ticksRun":120,"factions":[{"factionId":%q,"castleHealth":80,"unitTypes":[{"unitType":"knight","recruited":3}]},{"factionId":%q}]}`, factionA, factionA, factionB)
	insert := `INSERT INTO battle_runs(id,scenario_id,scenario_digest,scenario_snapshot,requested_ticks,owner_id,initial_state,current_state,admission_exempt,status,attempt,result,completed_at)
		VALUES($1,'frontier-siege','d',$2,500,'admin','{}','{}',true,$3,$4,$5,$6)`
	now := time.Now()
	exec(t, db, insert, recentBattle, snapshot("9"), "completed", 2, result, now)
	exec(t, db, insert, olderBattle, snapshot("8"), "completed", 1, result, now.Add(-240*time.Hour))
	exec(t, db, insert, runningOne, snapshot("9"), "running", 1, nil, nil)
	decision := `INSERT INTO battle_decisions(id,battle_id,attempt,faction_id,target_tick,status,controller_type,prompt_version,tool_schema_version,observation,observation_hash,usage,provider_metadata,created_at,resolved_at)
		VALUES($1,$2,$3,$4,$5,$6,'model','v','v','{}','h',$7,$8,$9,$10)`
	created := now.Add(-time.Hour)
	exec(t, db, decision, "00000000-0000-4000-8000-00000000d001", recentBattle, 2, factionA, 10, "accepted", `{"inputTokens":100,"cachedInputTokens":40,"outputTokens":10}`, `{"validationFailures":1}`, created, created.Add(2*time.Second))
	exec(t, db, decision, "00000000-0000-4000-8000-00000000d002", recentBattle, 2, factionA, 20, "fallback", `{}`, `{}`, created, created.Add(4*time.Second))
	exec(t, db, decision, "00000000-0000-4000-8000-00000000d003", recentBattle, 1, factionA, 10, "fallback", `{"inputTokens":999}`, `{}`, created, created)
	exec(t, db, `INSERT INTO leagues(id,owner_id,name,request_digest,battle_salt,scenario_ids,requested_ticks,group_count) VALUES($1,'admin','Cup','d',repeat('a',32),'{frontier-siege}',500,2)`, leagueID)
	exec(t, db, `INSERT INTO league_entrants(league_id,entrant_index,group_index,faction_id,faction_name,controller) VALUES($1,0,0,$2,'Amber','{}'),($1,1,0,$3,'Basalt','{}')`, leagueID, factionA, factionB)
	exec(t, db, `INSERT INTO league_fixtures(id,league_id,sequence,stage,group_index,round,leg,home_entrant,away_entrant,scenario_id,status,attempts,battle_id)
		VALUES('00000000-0000-4000-8000-00000000f001',$1,0,'group',0,1,1,0,1,'frontier-siege','completed',1,$2)`, leagueID, recentBattle)
}

// battleIDs lists the last digit of each selected battle ID, in order.
func battleIDs(t *testing.T, repo *Postgres, filter analyticsservice.Filter, limit int) string {
	t.Helper()
	battles, err := repo.CompletedBattles(context.Background(), filter, limit)
	if err != nil {
		t.Fatal(err)
	}
	result := ""
	for _, b := range battles {
		result += b.ID[len(b.ID)-1:]
	}
	return result
}

func TestCompletedBattleFiltersAgainstPostgres(t *testing.T) {
	db := privateDatabase(t)
	seed(t, db)
	repo := NewPostgres(db)
	since := time.Now().Add(-120 * time.Hour)
	for name, test := range map[string]struct {
		filter analyticsservice.Filter
		limit  int
		want   string
	}{
		"all, newest first": {analyticsservice.Filter{}, 10, "12"},
		"limit":             {analyticsservice.Filter{}, 1, "1"},
		"rules version":     {analyticsservice.Filter{RulesVersion: "8"}, 10, "2"},
		"unknown version":   {analyticsservice.Filter{RulesVersion: "none"}, 10, ""},
		"since":             {analyticsservice.Filter{Since: &since}, 10, "1"},
		"league":            {analyticsservice.Filter{LeagueID: leagueID}, 10, "1"},
		"other league":      {analyticsservice.Filter{LeagueID: factionA}, 10, ""},
	} {
		if got := battleIDs(t, repo, test.filter, test.limit); got != test.want {
			t.Errorf("%s: battles %q, want %q", name, got, test.want)
		}
	}
}

func TestCompletedBattleDecodingAgainstPostgres(t *testing.T) {
	db := privateDatabase(t)
	seed(t, db)
	battles, err := NewPostgres(db).CompletedBattles(context.Background(), analyticsservice.Filter{RulesVersion: "9"}, 10)
	if err != nil || len(battles) != 1 {
		t.Fatalf("battles = %+v, %v", battles, err)
	}
	b := battles[0]
	home, away := b.Sides[0], b.Sides[1]
	if b.RulesVersion != "9" || b.Reason != "castle_destroyed" || b.TicksRun != 120 || *b.WinnerFactionID != factionA || home.FactionName != "Amber" || home.Controller.ModelIdentifier != "sonnet" || away.Controller.Type != "scripted" {
		t.Fatalf("battle = %+v", b)
	}
	if home.Stats == nil || home.Stats.CastleHealth != 80 || len(home.Stats.UnitTypes) != 1 || away.Stats == nil {
		t.Fatalf("stats = %+v / %+v", home.Stats, away.Stats)
	}
	// Totals come from attempt 2 only; the attempt-1 decision is ignored.
	d := home.Decisions
	if d.Model != 2 || d.Fallbacks != 1 || d.InvalidPlans != 1 || d.Resolved != 2 || d.InputTokens != 100 || d.CachedInputTokens != 40 || d.OutputTokens != 10 || d.LatencyMS < 5999 || d.LatencyMS > 6001 {
		t.Fatalf("decisions = %+v", d)
	}
	if away.Decisions != (analytics.Decisions{}) {
		t.Fatalf("scripted side decisions = %+v", away.Decisions)
	}
}
