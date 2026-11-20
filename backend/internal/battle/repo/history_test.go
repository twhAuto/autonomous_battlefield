// Checks battle-summary row decoding, the rules-version statement and listing, and
// repository query outcomes.

package repo

import (
	"context"
	"strings"
	"testing"
	"time"

	battleservice "github.com/twhAuto/autonomous_battlefield/backend/internal/battle/service"
)

func historyRow(participants, outcome []byte) battleRow {
	return func(destinations ...any) error {
		return assignBattleValues(destinations, []any{"00000000-0000-0000-0000-000000000001", "scenario", "13", participants, "completed", false, uint64(25), uint64(10), time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC), (*time.Time)(nil), (*time.Time)(nil), outcome, "owner"})
	}
}

func TestListBattleSummaries(t *testing.T) {
	for _, test := range []struct {
		name string
		db   *battleDB
	}{
		{"summary", &battleDB{rows: &battleRows{scans: []battleRow{historyRow([]byte(`[{"id":"f1","name":"Original","controller":{"type":"scripted"}}]`), []byte(`{"winnerFactionId":"f1","reason":"castle_destroyed"}`))}}}},
		{"no outcome", &battleDB{rows: &battleRows{scans: []battleRow{historyRow([]byte("[]"), nil)}}}},
		{"empty", &battleDB{rows: &battleRows{}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := historyCursor()
			result, err := NewPostgres(test.db).ListBattles(context.Background(), battleservice.BattleHistoryQuery{Status: "completed", Before: before, Limit: 26})
			if err != nil || result == nil {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if test.db.queryArgs[1] != before.CreatedAt || test.db.queryArgs[2] != before.ID {
				t.Fatal(test.db.queryArgs)
			}
			if test.name == "summary" && (result[0].Participants[0].Name != "Original" || *result[0].Outcome.WinnerFactionID != "f1" || result[0].OwnerID != "owner" || result[0].RulesVersion != "13") {
				t.Fatal(result)
			}
		})
	}
}

func TestListBattleSummariesReportsEachFailure(t *testing.T) {
	for _, test := range []struct {
		name    string
		db      *battleDB
		wantErr string
	}{
		{"query", &battleDB{queryErr: errDatabase}, "list battles"},
		{"scan", &battleDB{rows: &battleRows{scans: []battleRow{func(...any) error { return errDatabase }}}}, "scan battle"},
		{"participants", &battleDB{rows: &battleRows{scans: []battleRow{historyRow([]byte("{"), nil)}}}, "decode battle participants"},
		{"outcome", &battleDB{rows: &battleRows{scans: []battleRow{historyRow([]byte("[]"), []byte("{"))}}}, "decode battle outcome"},
		{"iteration", &battleDB{rows: &battleRows{iterationErr: errDatabase}}, "iterate battle"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewPostgres(test.db).ListBattles(context.Background(), battleservice.BattleHistoryQuery{Status: "completed", Before: historyCursor(), Limit: 26})
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestListBattleSummariesWithoutCursorPassesNullBounds(t *testing.T) {
	db := &battleDB{rows: &battleRows{}}
	_, err := NewPostgres(db).ListBattles(context.Background(), battleservice.BattleHistoryQuery{Limit: 26})
	if err != nil || db.queryArgs[1] != nil || db.queryArgs[2] != nil {
		t.Fatalf("args=%+v err=%v", db.queryArgs, err)
	}
}

func TestListBattleSummariesUsesTheVersionStatementOnlyForAVersion(t *testing.T) {
	db := &battleDB{rows: &battleRows{}}
	if _, err := NewPostgres(db).ListBattles(context.Background(), battleservice.BattleHistoryQuery{Limit: 26}); err != nil || db.querySQL != listBattlesSQL || len(db.queryArgs) != 4 {
		t.Fatalf("unfiltered sql=%q args=%v err=%v", db.querySQL, db.queryArgs, err)
	}
	db.rows = &battleRows{}
	if _, err := NewPostgres(db).ListBattles(context.Background(), battleservice.BattleHistoryQuery{RulesVersion: "13", Limit: 26}); err != nil || db.querySQL != listBattlesByVersionSQL || len(db.queryArgs) != 5 || db.queryArgs[4] != "13" {
		t.Fatalf("version sql=%q args=%v err=%v", db.querySQL, db.queryArgs, err)
	}
}

func TestListRulesVersions(t *testing.T) {
	version := func(value string) battleRow {
		return func(destinations ...any) error { return assignBattleValues(destinations, []any{value}) }
	}
	db := &battleDB{rows: &battleRows{scans: []battleRow{version("13"), version("12")}}}
	versions, err := NewPostgres(db).ListRulesVersions(context.Background())
	if err != nil || len(versions) != 2 || versions[0] != "13" || versions[1] != "12" || db.querySQL != listRulesVersionsSQL {
		t.Fatalf("versions=%v err=%v", versions, err)
	}
	for name, db := range map[string]*battleDB{
		"query": {queryErr: errDatabase},
		"scan":  {rows: &battleRows{scans: []battleRow{func(...any) error { return errDatabase }}}},
	} {
		if _, err := NewPostgres(db).ListRulesVersions(context.Background()); err == nil || !strings.Contains(err.Error(), "list rules versions") {
			t.Fatalf("%s err=%v", name, err)
		}
	}
}

func historyCursor() *battleservice.BattleHistoryCursor {
	return &battleservice.BattleHistoryCursor{CreatedAt: time.Now(), ID: "00000000-0000-0000-0000-000000000002"}
}
