// Checks migration 00024: it refuses to run while a league is running, deletes leagues
// in the earlier format while keeping their battles, drops fixture legs, accepts only
// dual-tournament and single-tie fixture numbers, and its downgrade refuses running
// leagues too.

package migrations_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/postgres"
)

// insertLeague adds a running two-group league with entrants 0 and 1 in group A.
func insertLeague(t *testing.T, db *sql.DB) string {
	t.Helper()
	id := randomUUID(t)
	execSQL(t, db, `INSERT INTO leagues(id,owner_id,name,request_digest,battle_salt,scenario_ids,requested_ticks,group_count) VALUES($1,'admin','Cup','d',repeat('a',32),'{frontier-siege}',500,2)`, id)
	for entrant := range 2 {
		execSQL(t, db, `INSERT INTO league_entrants(league_id,entrant_index,group_index,faction_id,faction_name,controller) VALUES($1,$2,0,$3,'F','{}')`, id, entrant, randomUUID(t))
	}
	return id
}

func execSQL(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatal(err)
	}
}

func count(t *testing.T, db *sql.DB, query string) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(), query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestLeagueFormatMigrationDropsEarlierLeagues(t *testing.T) {
	db, _ := privateSchema(t)
	ctx := context.Background()
	provider, err := postgres.NewMigrationProvider(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 23); err != nil {
		t.Fatal(err)
	}
	old, battle := insertLeague(t, db), randomUUID(t)
	execSQL(t, db, `INSERT INTO battle_runs(id,scenario_id,scenario_digest,scenario_snapshot,requested_ticks,owner_id,initial_state,current_state) VALUES($1,'s','d','{}',10,'admin','{}','{}')`, battle)
	execSQL(t, db, `INSERT INTO league_fixtures(id,league_id,sequence,stage,group_index,round,leg,home_entrant,away_entrant,scenario_id,battle_id) VALUES($1,$2,0,'group',0,4,2,0,1,'frontier-siege',$3)`, randomUUID(t), old, battle)
	if _, err = provider.UpByOne(ctx); err == nil {
		t.Fatal("the format change dropped a running league")
	}
	execSQL(t, db, `UPDATE leagues SET status='cancelled', completed_at=now() WHERE id=$1`, old)
	if _, err = provider.UpByOne(ctx); err != nil {
		t.Fatalf("format change with only settled leagues: %v", err)
	}
	if leagues, battles := count(t, db, `SELECT count(*) FROM leagues`), count(t, db, `SELECT count(*) FROM battle_runs`); leagues != 0 || battles != 1 {
		t.Fatalf("%d leagues and %d battles remain; want only the battle", leagues, battles)
	}
	if legs := count(t, db, `SELECT count(*) FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'league_fixtures' AND column_name = 'leg'`); legs != 0 {
		t.Fatal("fixtures still have legs")
	}
	requireFixtureNumbers(t, db, insertLeague(t, db))
	if _, err = provider.Down(ctx); err == nil {
		t.Fatal("the downgrade dropped a running league")
	}
	execSQL(t, db, `UPDATE leagues SET status='cancelled', completed_at=now()`)
	if _, err = provider.Down(ctx); err != nil || count(t, db, `SELECT count(*) FROM leagues`) != 0 {
		t.Fatalf("downgrade with only settled leagues: %v", err)
	}
}

// requireFixtureNumbers checks which stage, round and tie combinations a fixture may have.
func requireFixtureNumbers(t *testing.T, db *sql.DB, leagueID string) {
	t.Helper()
	for i, test := range []struct {
		stage      string
		round, tie int
		allowed    bool
		inGroup    bool
	}{
		{"group", 1, 2, true, true}, {"group", 3, 1, true, true}, {"semifinal", 1, 2, true, false},
		{"group", 4, 1, false, true}, {"group", 2, 3, false, true}, {"group", 1, 0, false, true}, {"final", 2, 1, false, false},
	} {
		var group any
		if test.inGroup {
			group = 0
		}
		_, err := db.ExecContext(context.Background(), `INSERT INTO league_fixtures(id,league_id,sequence,stage,group_index,round,tie,home_entrant,away_entrant,scenario_id) VALUES($1,$2,$3,$4,$5,$6,$7,0,1,'frontier-siege')`,
			randomUUID(t), leagueID, i, test.stage, group, test.round, test.tie)
		if (err == nil) != test.allowed {
			t.Errorf("%s round %d tie %d: err %v, want allowed %v", test.stage, test.round, test.tie, err, test.allowed)
		}
	}
}
