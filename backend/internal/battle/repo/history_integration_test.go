// Exercises history queries, rules-version filtering and listing, and the reversible
// history-index migration against PostgreSQL.

package repo

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	battleservice "github.com/twhAuto/autonomous_battlefield/backend/internal/battle/service"
	battle "github.com/twhAuto/autonomous_battlefield/backend/internal/game"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/postgres"
)

func TestBattleHistoryAgainstPostgres(t *testing.T) {
	ctx, db := integrationDatabase(t)
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	// Isolate fixtures from other integration tests and any existing development data.
	mustExec(t, tx, "CREATE TEMP TABLE battle_runs (LIKE battle_runs INCLUDING ALL) ON COMMIT DROP")
	repo := NewPostgres(tx)
	scenario := repositoryTestScenario()
	scenario.Factions[0].Name = "Historical name"
	when := time.Date(2026, 9, 7, 0, 0, 0, 123456000, time.UTC)
	ids := seedHistory(t, tx, repo, scenario, when)

	checkHistoryStatusFilters(t, repo, ids)
	checkHistoryIgnoresFactionRegistry(t, tx, repo, scenario.Factions[0].ID)
	checkHistoryCursorIgnoresNewerBattles(t, tx, repo, scenario, when, ids)
	checkHistoryRulesVersions(t, tx, repo, scenario, when)
}

// checkHistoryRulesVersions adds a battle on another rules version: each version filter
// returns only its own battles, still newest first, and the listing names both.
func checkHistoryRulesVersions(t *testing.T, tx pgx.Tx, repo *Postgres, scenario battle.Scenario, when time.Time) {
	t.Helper()
	ctx := context.Background()
	scenario.RulesVersion = "2"
	newerID := "00000000-0000-0000-0000-000000000009"
	createHistoryBattle(t, repo, scenario, newerID)
	mustExec(t, tx, "UPDATE battle_runs SET created_at=$2 WHERE id=$1", newerID, when.Add(2*time.Second))
	rows, err := repo.ListBattles(ctx, battleservice.BattleHistoryQuery{RulesVersion: "2", Limit: 100})
	if err != nil || len(rows) != 1 || rows[0].ID != newerID || rows[0].RulesVersion != "2" {
		t.Fatalf("version 2 rows=%+v err=%v", rows, err)
	}
	rows, err = repo.ListBattles(ctx, battleservice.BattleHistoryQuery{RulesVersion: "1", Status: "completed", Limit: 100})
	if err != nil || len(rows) != 1 || rows[0].RulesVersion != "1" {
		t.Fatalf("version 1 completed rows=%+v err=%v", rows, err)
	}
	rows, err = repo.ListBattles(ctx, battleservice.BattleHistoryQuery{RulesVersion: "1", Limit: 100})
	if err != nil || len(rows) != 8 {
		t.Fatalf("version 1 rows=%d err=%v", len(rows), err)
	}
	versions, err := repo.ListRulesVersions(ctx)
	slices.SortFunc(versions, battle.CompareRulesVersions)
	if err != nil || !slices.Equal(versions, []string{"2", "1"}) {
		t.Fatalf("versions=%v err=%v", versions, err)
	}
}

// seedHistory creates seven battles, one per status (two of them paused), all at the
// same creation time, and returns their IDs in creation order.
func seedHistory(t *testing.T, tx pgx.Tx, repo *Postgres, scenario battle.Scenario, when time.Time) []string {
	t.Helper()
	statuses := []string{"queued", "running", "queued", "running", "completed", "failed", "stopped"}
	ids := make([]string, len(statuses))
	for i, status := range statuses {
		ids[i] = fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1)
		createHistoryBattle(t, repo, scenario, ids[i])
		mustExec(t, tx, "UPDATE battle_runs SET status=$2, paused=$3, created_at=$4 WHERE id=$1", ids[i], status, i == 2 || i == 3, when)
	}
	mustExec(t, tx, `UPDATE battle_runs SET result='{"winnerFactionId":null,"reason":"tick_limit","factions":[]}' WHERE status='completed'`)
	return ids
}

func createHistoryBattle(t *testing.T, repo *Postgres, scenario battle.Scenario, id string) {
	t.Helper()
	if err := repo.CreateBattle(context.Background(), battleservice.CreateBattleParams{ID: id, ScenarioID: scenario.ID, ScenarioDigest: "digest", ScenarioSnapshot: scenario, RequestedTicks: 25}); err != nil {
		t.Fatal(err)
	}
}

// checkHistoryStatusFilters lists each status filter, newest ID first among equal times.
func checkHistoryStatusFilters(t *testing.T, repo *Postgres, ids []string) {
	t.Helper()
	for _, test := range []struct {
		status string
		want   []int
	}{
		{"", []int{6, 5, 4, 3, 2, 1, 0}}, {"queued", []int{0}}, {"running", []int{1}}, {"paused", []int{3, 2}}, {"completed", []int{4}}, {"failed", []int{5}}, {"stopped", []int{6}},
	} {
		rows, err := repo.ListBattles(context.Background(), battleservice.BattleHistoryQuery{Status: test.status, Limit: 100})
		if err != nil || len(rows) != len(test.want) {
			t.Fatalf("status=%s rows=%+v err=%v", test.status, rows, err)
		}
		for i, index := range test.want {
			if rows[i].ID != ids[index] {
				t.Fatal(rows)
			}
		}
	}
}

// checkHistoryIgnoresFactionRegistry renames and then deletes the faction: the saved
// snapshot keeps the original name and outcome.
func checkHistoryIgnoresFactionRegistry(t *testing.T, tx pgx.Tx, repo *Postgres, factionID string) {
	t.Helper()
	mustExec(t, tx, "CREATE TEMP TABLE factions (id text, name text) ON COMMIT DROP")
	mustExec(t, tx, "INSERT INTO factions VALUES ($1,'Historical name')", factionID)
	for _, mutation := range []string{"UPDATE factions SET name='Renamed'", "DELETE FROM factions"} {
		mustExec(t, tx, mutation)
		rows, err := repo.ListBattles(context.Background(), battleservice.BattleHistoryQuery{Status: "completed", Limit: 25})
		if err != nil || rows[0].Participants[0].Name != "Historical name" || rows[0].Outcome == nil || rows[0].Outcome.WinnerFactionID != nil || rows[0].Outcome.Reason != "tick_limit" {
			t.Fatalf("rows=%+v err=%v", rows, err)
		}
	}
}

// checkHistoryCursorIgnoresNewerBattles pages through the history two at a time while a
// newer battle is created after the first page: the cursor still returns exactly the
// original seven, and a fresh first page starts with the newer battle.
func checkHistoryCursorIgnoresNewerBattles(t *testing.T, tx pgx.Tx, repo *Postgres, scenario battle.Scenario, when time.Time, ids []string) {
	t.Helper()
	ctx := context.Background()
	service := battleservice.NewService(repo, nil, nil, 100, battleservice.Options{})
	first, err := service.ListBattles(ctx, battleservice.BattleHistoryInput{Limit: 2})
	if err != nil || first.NextCursor == nil || first.Battles[0].ID != ids[6] {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	newerID := "00000000-0000-0000-0000-000000000008"
	createHistoryBattle(t, repo, scenario, newerID)
	mustExec(t, tx, "UPDATE battle_runs SET created_at=$2 WHERE id=$1", newerID, when.Add(time.Second))
	seen := []string{first.Battles[0].ID, first.Battles[1].ID}
	for cursor := first.NextCursor; cursor != nil; {
		page, err := service.ListBattles(ctx, battleservice.BattleHistoryInput{Limit: 2, Cursor: *cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range page.Battles {
			seen = append(seen, row.ID)
		}
		cursor = page.NextCursor
	}
	want := []string{ids[6], ids[5], ids[4], ids[3], ids[2], ids[1], ids[0]}
	if !slices.Equal(seen, want) {
		t.Fatal(seen)
	}
	fresh, err := service.ListBattles(ctx, battleservice.BattleHistoryInput{Limit: 1})
	if err != nil || fresh.Battles[0].ID != newerID {
		t.Fatalf("fresh=%+v err=%v", fresh, err)
	}
}

// mustExec runs a fixture statement and stops the test if it fails.
func mustExec(t *testing.T, db postgres.DBTX, sql string, args ...any) {
	t.Helper()
	if _, err := db.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(sql, err)
	}
}

func TestBattleHistoryMigrationUpAndDown(t *testing.T) {
	ctx, pool := integrationDatabase(t)
	config := pool.Config().ConnConfig.Copy()
	admin := stdlib.OpenDB(*config)
	defer admin.Close()
	schema := "history_" + strings.ReplaceAll(randomUUID(t), "-", "")
	{
		_, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema)
		if err != nil {
			t.Fatal(err)
		}
	}
	defer admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	config.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*config)
	defer db.Close()
	provider, err := postgres.NewMigrationProvider(db)
	if err != nil {
		t.Fatal(err)
	}
	{
		_, err := provider.UpTo(ctx, 9)
		if err != nil {
			t.Fatal(err)
		}
	}
	id := randomUUID(t)
	{
		_, err := db.ExecContext(ctx, `INSERT INTO battle_runs(id,scenario_id,scenario_digest,scenario_snapshot,requested_ticks,initial_state,current_state) VALUES($1,'old','digest','{"factions":[]}',25,'{}','{}')`, id)
		if err != nil {
			t.Fatal(err)
		}
	}
	{
		_, err := provider.UpTo(ctx, 10)
		if err != nil {
			t.Fatal(err)
		}
	}
	var index string
	{
		err := db.QueryRowContext(ctx, "SELECT indexdef FROM pg_indexes WHERE schemaname=$1 AND indexname='battle_runs_history_idx'", schema).Scan(&index)
		if err != nil || !strings.Contains(index, "created_at DESC, id DESC") {
			t.Fatalf("index=%s err=%v", index, err)
		}
	}
	{
		_, err := provider.Down(ctx)
		if err != nil {
			t.Fatal(err)
		}
	}
	var count int
	{
		err := db.QueryRowContext(ctx, "SELECT count(*) FROM battle_runs WHERE id=$1", id).Scan(&count)
		if err != nil || count != 1 {
			t.Fatalf("count=%d err=%v", count, err)
		}
	}
	{
		err := db.QueryRowContext(ctx, "SELECT indexdef FROM pg_indexes WHERE schemaname=$1 AND indexname='battle_runs_history_idx'", schema).Scan(&index)
		if err == nil {
			t.Fatal("history index survived rollback")
		}
	}
	{
		_, err := provider.UpTo(ctx, 10)
		if err != nil {
			t.Fatal(err)
		}
	}
}

// Cached prepared statements switch to generic plans, where an "IS NULL OR" cursor
// becomes a filter and deep pages scan every newer row. With sequential scans and
// sorts disabled, the history index is chosen regardless of table size, so the test
// checks only where the cursor predicate lands.
func TestBattleHistoryCursorStaysIndexConditionInGenericPlans(t *testing.T) {
	ctx, db := integrationDatabase(t)
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	for _, statement := range []string{
		"SET LOCAL plan_cache_mode = force_generic_plan",
		"SET LOCAL enable_seqscan = off",
		"SET LOCAL enable_sort = off",
		"PREPARE history_plan(text, timestamptz, uuid, int) AS " + listBattlesSQL,
	} {
		_, err := tx.Exec(ctx, statement)
		if err != nil {
			t.Fatal(statement, err)
		}
	}
	rows, err := tx.Query(ctx, "EXPLAIN (COSTS OFF) EXECUTE history_plan('', now(), gen_random_uuid(), 26)")
	if err != nil {
		t.Fatal(err)
	}
	var plan strings.Builder
	for rows.Next() {
		var line string
		if rows.Scan(&line) != nil {
			t.Fatal("scan plan line")
		}
		plan.WriteString(line + "\n")
	}
	rows.Close()
	text := plan.String()
	if !strings.Contains(text, "battle_runs_history_idx") || !strings.Contains(text, "Index Cond: (ROW(created_at, id) <") {
		t.Fatalf("history cursor is not an index condition in the generic plan:\n%s", text)
	}
}

// The rules-version statement must keep both the version and the cursor as index
// conditions in generic plans, so an old version's pages do not scan newer battles.
func TestBattleHistoryRulesVersionStaysIndexConditionInGenericPlans(t *testing.T) {
	ctx, db := integrationDatabase(t)
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	for _, statement := range []string{
		"SET LOCAL plan_cache_mode = force_generic_plan",
		"SET LOCAL enable_seqscan = off",
		"SET LOCAL enable_sort = off",
		"PREPARE version_plan(text, timestamptz, uuid, int, text) AS " + listBattlesByVersionSQL,
	} {
		if _, err := tx.Exec(ctx, statement); err != nil {
			t.Fatal(statement, err)
		}
	}
	var plan strings.Builder
	rows, err := tx.Query(ctx, "EXPLAIN (COSTS OFF) EXECUTE version_plan('', now(), gen_random_uuid(), 26, '13')")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var line string
		if rows.Scan(&line) != nil {
			t.Fatal("scan plan line")
		}
		plan.WriteString(line + "\n")
	}
	rows.Close()
	text := plan.String()
	if !strings.Contains(text, "battle_runs_rules_version_history_idx") || !strings.Contains(text, "rules_version = $5") || !strings.Contains(text, "ROW(created_at, id) <") || strings.Contains(text, "Filter: (rules_version") {
		t.Fatalf("rules version or cursor is not an index condition in the generic plan:\n%s", text)
	}
}
