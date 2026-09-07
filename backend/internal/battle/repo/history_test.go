// Checks battle-summary row decoding and repository query outcomes.

package repo

import (
	"context"
	"strings"
	"testing"
	"time"

	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
)

func historyRow(participants, outcome []byte) battleRow {
	return func(destinations ...any) error {
		return assignBattleValues(destinations, []any{"00000000-0000-0000-0000-000000000001", "scenario", participants, "completed", false, uint64(25), uint64(10), time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC), (*time.Time)(nil), (*time.Time)(nil), outcome})
	}
}
func TestListBattleSummaries(t *testing.T) {
	for _, test := range []struct {
		name    string
		db      *battleDB
		wantErr string
	}{
		{"summary", &battleDB{rows: &battleRows{scans: []battleRow{historyRow([]byte(`[{"id":"f1","name":"Original","controller":{"type":"scripted"}}]`), []byte(`{"winnerFactionId":"f1","reason":"castle_destroyed"}`))}}}, ""},
		{"no outcome", &battleDB{rows: &battleRows{scans: []battleRow{historyRow([]byte("[]"), nil)}}}, ""},
		{"empty", &battleDB{rows: &battleRows{}}, ""},
		{"query", &battleDB{queryErr: errDatabase}, "list battles"},
		{"scan", &battleDB{rows: &battleRows{scans: []battleRow{func(...any) error { return errDatabase }}}}, "scan battle"},
		{"participants", &battleDB{rows: &battleRows{scans: []battleRow{historyRow([]byte("{"), nil)}}}, "decode battle participants"},
		{"outcome", &battleDB{rows: &battleRows{scans: []battleRow{historyRow([]byte("[]"), []byte("{"))}}}, "decode battle outcome"},
		{"iteration", &battleDB{rows: &battleRows{iterationErr: errDatabase}}, "iterate battle"},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := &battleservice.BattleHistoryCursor{CreatedAt: time.Now(), ID: "00000000-0000-0000-0000-000000000002"}
			result, err := NewPostgres(test.db).ListBattles(context.Background(), battleservice.BattleHistoryQuery{Status: "completed", Before: before, Limit: 26})
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("err=%v", err)
				}
				return
			}
			if err != nil || result == nil {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if test.db.queryArgs[1] != before.CreatedAt || test.db.queryArgs[2] != before.ID {
				t.Fatal(test.db.queryArgs)
			}
			if test.name == "summary" && (result[0].Participants[0].Name != "Original" || *result[0].Outcome.WinnerFactionID != "f1") {
				t.Fatal(result)
			}
		})
	}
	db := &battleDB{rows: &battleRows{}}
	_, err := NewPostgres(db).ListBattles(context.Background(), battleservice.BattleHistoryQuery{Limit: 26})
	if err != nil || db.queryArgs[1] != nil || db.queryArgs[2] != nil {
		t.Fatalf("args=%+v err=%v", db.queryArgs, err)
	}
}
