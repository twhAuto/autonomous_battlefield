// Checks the battle allowance read against PostgreSQL: the role's limits from
// runtime_policy and the same per-owner counts the admission trigger uses.

package repo

import (
	"context"
	"testing"
	"time"

	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
)

func TestBattleAllowanceAgainstPostgres(t *testing.T) {
	ctx, db := integrationDatabase(t)
	owner := "allowance-" + randomUUID(t)
	t.Cleanup(func() { _, _ = db.Exec(context.Background(), `DELETE FROM battle_runs WHERE owner_id = $1`, owner) })
	// Exempt rows pass the trigger's limits but still count for their owner.
	insert := func(createdAt string) string {
		t.Helper()
		id := randomUUID(t)
		if _, err := db.Exec(ctx, `INSERT INTO battle_runs(id,scenario_id,scenario_digest,scenario_snapshot,requested_ticks,owner_id,initial_state,current_state,admission_exempt,created_at)
			VALUES($1,'s','d','{}',10,$2,'{}','{}',true,`+createdAt+`)`, id, owner); err != nil {
			t.Fatal(err)
		}
		return id
	}
	insert("now()")
	finished := insert("now()")
	yesterday := insert("now() - interval '1 day'")
	if _, err := db.Exec(ctx, `UPDATE battle_runs SET status='completed' WHERE id = ANY($1)`, []string{finished, yesterday}); err != nil {
		t.Fatal(err)
	}

	var playerDaily, playerUnfinished, testerDaily, testerUnfinished, global int
	if err := db.QueryRow(ctx, `SELECT player_daily_battles,player_unfinished,tester_daily_battles,tester_unfinished,global_unfinished FROM runtime_policy WHERE id`).
		Scan(&playerDaily, &playerUnfinished, &testerDaily, &testerUnfinished, &global); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		tester bool
		want   battleservice.Allowance
	}{
		{false, battleservice.Allowance{DailyLimit: playerDaily, UnfinishedLimit: playerUnfinished, ServiceUnfinishedLimit: global}},
		{true, battleservice.Allowance{DailyLimit: testerDaily, UnfinishedLimit: testerUnfinished, ServiceUnfinishedLimit: global}},
	} {
		got, err := NewPostgres(db).BattleAllowance(ctx, owner, tc.tester)
		if err != nil {
			t.Fatal(err)
		}
		checkAllowance(t, got, tc.want)
	}
}

// checkAllowance compares an owner's allowance with two of today's battles, one of them
// unfinished, and the wanted limits.
func checkAllowance(t *testing.T, got, want battleservice.Allowance) {
	t.Helper()
	if !got.Limited || got.StartedToday != 2 || got.Unfinished != 1 || got.DailyLimit != want.DailyLimit || got.UnfinishedLimit != want.UnfinishedLimit {
		t.Fatalf("allowance = %#v; want limits %#v", got, want)
	}
	// Other tests share this database, so only the cap and a lower bound are fixed.
	if got.ServiceUnfinishedLimit != want.ServiceUnfinishedLimit || got.ServiceUnfinished < 1 {
		t.Fatalf("service counts = %d of %d", got.ServiceUnfinished, got.ServiceUnfinishedLimit)
	}
	reset := got.ResetAt.UTC()
	if until := time.Until(reset); until <= 0 || until > 24*time.Hour || !reset.Equal(reset.Truncate(24*time.Hour)) {
		t.Fatalf("resetAt = %s; want the next UTC midnight", reset)
	}
}
