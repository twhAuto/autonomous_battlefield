// Exercises history queries and reversible history-index migration against PostgreSQL.

package repo

import (
	"github.com/twh09/autonomous_battlefield/backend/internal/testutil"

	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twh09/autonomous_battlefield/backend/migrations"
)

func TestBattleHistoryAgainstPostgres(t *testing.T) {
	ctx, db := integrationDatabase(t)
	tx, err := db.Begin(ctx)
	testutil.Require(t, err == nil, func() { t.Fatal(err) })
	defer tx.Rollback(context.Background())
	// Isolate fixtures from other integration tests and any existing development data.
	{
		_, err := tx.Exec(ctx, "CREATE TEMP TABLE battle_runs (LIKE battle_runs INCLUDING ALL) ON COMMIT DROP")
		testutil.Require(t, err == nil, func() { t.Fatal(err) })
	}
	repo := NewPostgres(tx)
	scenario := repositoryTestScenario()
	scenario.Factions[0].Name = "Historical name"
	when := time.Date(2026, 9, 7, 0, 0, 0, 123456000, time.UTC)
	statuses := []string{"queued", "running", "queued", "running", "completed", "failed", "stopped"}
	ids := make([]string, len(statuses))
	for i, status := range statuses {
		ids[i] = fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1)
		{
			err := repo.CreateBattle(ctx, battleservice.CreateBattleParams{ID: ids[i], ScenarioID: scenario.ID, ScenarioDigest: "digest", ScenarioSnapshot: scenario, RequestedTicks: 25})
			testutil.Require(t, err == nil, func() { t.Fatal(err) })
		}
		{
			_, err := tx.Exec(ctx, "UPDATE battle_runs SET status=$2, paused=$3, created_at=$4 WHERE id=$1", ids[i], status, i == 2 || i == 3, when)
			testutil.Require(t, err == nil, func() { t.Fatal(err) })
		}
	}
	{
		_, err := tx.Exec(ctx, `UPDATE battle_runs SET result='{"winnerFactionId":null,"reason":"tick_limit","factions":[]}' WHERE status='completed'`)
		testutil.Require(t, err == nil, func() { t.Fatal(err) })
	}
	for _, test := range []struct {
		status string
		want   []int
	}{
		{"", []int{6, 5, 4, 3, 2, 1, 0}}, {"queued", []int{0}}, {"running", []int{1}}, {"paused", []int{3, 2}}, {"completed", []int{4}}, {"failed", []int{5}}, {"stopped", []int{6}},
	} {
		rows, err := repo.ListBattles(ctx, battleservice.BattleHistoryQuery{Status: test.status, Limit: 100})
		testutil.Require(t, !(err != nil || len(rows) != len(test.want)), func() { t.Fatalf("status=%s rows=%+v err=%v", test.status, rows, err) })
		for i, index := range test.want {
			testutil.Require(t, rows[i].ID == ids[index], func() { t.Fatal(rows) })
		}
	}
	// A faction registry change cannot affect the saved snapshot, including after deletion.
	{
		_, err := tx.Exec(ctx, "CREATE TEMP TABLE factions (id text, name text) ON COMMIT DROP")
		testutil.Require(t, err == nil, func() { t.Fatal(err) })
	}
	{
		_, err := tx.Exec(ctx, "INSERT INTO factions VALUES ($1,'Historical name')", scenario.Factions[0].ID)
		testutil.Require(t, err == nil, func() { t.Fatal(err) })
	}
	for _, mutation := range []string{"UPDATE factions SET name='Renamed'", "DELETE FROM factions"} {
		{
			_, err := tx.Exec(ctx, mutation)
			testutil.Require(t, err == nil, func() { t.Fatal(err) })
		}
		rows, err := repo.ListBattles(ctx, battleservice.BattleHistoryQuery{Status: "completed", Limit: 25})
		testutil.Require(t, !(err != nil || rows[0].Participants[0].Name != "Historical name" || rows[0].Outcome == nil || rows[0].Outcome.WinnerFactionID != nil || rows[0].Outcome.Reason != "tick_limit"), func() { t.Fatalf("rows=%+v err=%v", rows, err) })
	}
	service := battleservice.NewService(repo, nil, nil, 100)
	first, err := service.ListBattles(ctx, battleservice.BattleHistoryInput{Limit: 2})
	testutil.Require(t, !(err != nil || first.NextCursor == nil || first.Battles[0].ID != ids[6]), func() { t.Fatalf("first=%+v err=%v", first, err) })
	newerID := "00000000-0000-0000-0000-000000000008"
	{
		err := repo.CreateBattle(ctx, battleservice.CreateBattleParams{ID: newerID, ScenarioID: scenario.ID, ScenarioDigest: "digest", ScenarioSnapshot: scenario, RequestedTicks: 25})
		testutil.Require(t, err == nil, func() { t.Fatal(err) })
	}
	{
		_, err := tx.Exec(ctx, "UPDATE battle_runs SET created_at=$2 WHERE id=$1", newerID, when.Add(time.Second))
		testutil.Require(t, err == nil, func() { t.Fatal(err) })
	}
	seen := []string{first.Battles[0].ID, first.Battles[1].ID}
	cursor := first.NextCursor
	for cursor != nil {
		page, err := service.ListBattles(ctx, battleservice.BattleHistoryInput{Limit: 2, Cursor: *cursor})
		testutil.Require(t, err == nil, func() { t.Fatal(err) })
		for _, row := range page.Battles {
			seen = append(seen, row.ID)
		}
		cursor = page.NextCursor
	}
	testutil.Require(t, len(seen) == 7, func() { t.Fatal(seen) })
	for i, id := range seen {
		testutil.Require(t, id == ids[6-i], func() { t.Fatal(seen) })
	}
	fresh, err := service.ListBattles(ctx, battleservice.BattleHistoryInput{Limit: 1})
	testutil.Require(t, !(err != nil || fresh.Battles[0].ID != newerID), func() { t.Fatalf("fresh=%+v err=%v", fresh, err) })
}

func TestBattleHistoryMigrationUpAndDown(t *testing.T) {
	ctx, pool := integrationDatabase(t)
	config := pool.Config().ConnConfig.Copy()
	admin := stdlib.OpenDB(*config)
	defer admin.Close()
	schema := "history_" + strings.ReplaceAll(randomUUID(t), "-", "")
	{
		_, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema)
		testutil.Require(t, err == nil, func() { t.Fatal(err) })
	}
	defer admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	config.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*config)
	defer db.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.Files)
	testutil.Require(t, err == nil, func() { t.Fatal(err) })
	{
		_, err := provider.UpTo(ctx, 9)
		testutil.Require(t, err == nil, func() { t.Fatal(err) })
	}
	id := randomUUID(t)
	{
		_, err := db.ExecContext(ctx, `INSERT INTO battle_runs(id,scenario_id,scenario_digest,scenario_snapshot,requested_ticks,initial_state,current_state) VALUES($1,'old','digest','{"factions":[]}',25,'{}','{}')`, id)
		testutil.Require(t, err == nil, func() { t.Fatal(err) })
	}
	{
		_, err := provider.UpTo(ctx, 10)
		testutil.Require(t, err == nil, func() { t.Fatal(err) })
	}
	var index string
	{
		err := db.QueryRowContext(ctx, "SELECT indexdef FROM pg_indexes WHERE schemaname=$1 AND indexname='battle_runs_history_idx'", schema).Scan(&index)
		testutil.Require(t, !(err != nil || !strings.Contains(index, "created_at DESC, id DESC")), func() { t.Fatalf("index=%s err=%v", index, err) })
	}
	{
		_, err := provider.Down(ctx)
		testutil.Require(t, err == nil, func() { t.Fatal(err) })
	}
	var count int
	{
		err := db.QueryRowContext(ctx, "SELECT count(*) FROM battle_runs WHERE id=$1", id).Scan(&count)
		testutil.Require(t, !(err != nil || count != 1), func() { t.Fatalf("count=%d err=%v", count, err) })
	}
	{
		err := db.QueryRowContext(ctx, "SELECT indexdef FROM pg_indexes WHERE schemaname=$1 AND indexname='battle_runs_history_idx'", schema).Scan(&index)
		testutil.Require(t, err != nil, func() { t.Fatal("history index survived rollback") })
	}
	{
		_, err := provider.UpTo(ctx, 10)
		testutil.Require(t, err == nil, func() { t.Fatal(err) })
	}
}
