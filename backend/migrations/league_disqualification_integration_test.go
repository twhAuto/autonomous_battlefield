// Checks migration 00025: a disqualification names who made it and keeps a short reason,
// a fixture is forfeited exactly when it names the forfeiting side, and the downgrade
// deletes leagues with a disqualified entrant, keeping their battles, once those leagues
// have stopped and none of their battles is queued or running.

package migrations_test

import (
	"context"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/postgres"
)

func TestLeagueDisqualificationMigration(t *testing.T) {
	db, _ := privateSchema(t)
	ctx := context.Background()
	provider, err := postgres.NewMigrationProvider(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 25); err != nil {
		t.Fatal(err)
	}
	kept, disqualified, battle := insertLeague(t, db), insertLeague(t, db), randomUUID(t)
	execSQL(t, db, `INSERT INTO battle_runs(id,scenario_id,scenario_digest,scenario_snapshot,requested_ticks,owner_id,initial_state,current_state) VALUES($1,'s','d','{}',10,'admin','{}','{}')`, battle)
	for _, test := range []struct {
		name    string
		query   string
		args    []any
		allowed bool
	}{
		{"disqualified by nobody", `UPDATE league_entrants SET disqualified_at = now() WHERE league_id = $1 AND entrant_index = 0`, []any{disqualified}, false},
		{"reason without a disqualification", `UPDATE league_entrants SET disqualification_reason = 'slow' WHERE league_id = $1 AND entrant_index = 0`, []any{disqualified}, false},
		{"reason too long", `UPDATE league_entrants SET disqualified_at = now(), disqualified_by = 'admin', disqualification_reason = repeat('x', 201) WHERE league_id = $1 AND entrant_index = 0`, []any{disqualified}, false},
		{"disqualification", `UPDATE league_entrants SET disqualified_at = now(), disqualified_by = 'admin', disqualification_reason = 'slow' WHERE league_id = $1 AND entrant_index = 0`, []any{disqualified}, true},
		{"forfeited without a side", fixtureSQL("'forfeited'", "NULL"), []any{randomUUID(t), disqualified, nil}, false},
		{"side without a forfeit", fixtureSQL("'pending'", "'home'"), []any{randomUUID(t), disqualified, nil}, false},
		{"unknown side", fixtureSQL("'forfeited'", "'nobody'"), []any{randomUUID(t), disqualified, nil}, false},
		{"forfeited battle", fixtureSQL("'forfeited'", "'home'"), []any{randomUUID(t), disqualified, battle}, true},
	} {
		if _, err := db.ExecContext(ctx, test.query, test.args...); (err == nil) != test.allowed {
			t.Errorf("%s: err %v, want allowed %v", test.name, err, test.allowed)
		}
	}
	execSQL(t, db, `UPDATE leagues SET status = 'cancelled', completed_at = now() WHERE id = $1`, kept)
	if _, err = provider.Down(ctx); err == nil {
		t.Fatal("the downgrade dropped a running league with a disqualified entrant")
	}
	execSQL(t, db, `UPDATE leagues SET status = 'cancelled', completed_at = now() WHERE id = $1`, disqualified)
	// The forfeited battle has not stopped yet, so the scheduler still needs its fixture.
	if _, err = provider.Down(ctx); err == nil {
		t.Fatal("the downgrade dropped a league whose forfeited battle still runs")
	}
	execSQL(t, db, `UPDATE battle_runs SET status = 'stopped', completed_at = now() WHERE id = $1`, battle)
	if _, err = provider.Down(ctx); err != nil {
		t.Fatalf("downgrade with only stopped leagues: %v", err)
	}
	if leagues, battles := count(t, db, `SELECT count(*) FROM leagues`), count(t, db, `SELECT count(*) FROM battle_runs`); leagues != 1 || battles != 1 {
		t.Fatalf("%d leagues and %d battles remain; want the league without a disqualification and the battle", leagues, battles)
	}
}

// fixtureSQL inserts a group A opening between entrants 0 and 1 with the given status
// and forfeit; its arguments are the fixture ID, league ID and battle ID.
func fixtureSQL(status, forfeit string) string {
	return `INSERT INTO league_fixtures(id,league_id,sequence,stage,group_index,round,tie,home_entrant,away_entrant,scenario_id,status,battle_id,forfeit)
		VALUES($1,$2,(SELECT count(*) FROM league_fixtures),'group',0,1,1,0,1,'frontier-siege',` + status + `,$3,` + forfeit + `)`
}
