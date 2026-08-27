package repo

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/twh09/autonomous_battlefield/backend/internal/battle/engine/worker"
	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/server/service"
	battle "github.com/twh09/autonomous_battlefield/backend/internal/game"
)

var errDatabase = errors.New("database unavailable")

func TestCreateBattlePersistsSnapshotAndMapsErrors(t *testing.T) {
	params := battleservice.CreateBattleParams{
		ID: "battle-1", ScenarioID: "scenario-1", ScenarioDigest: "digest",
		ScenarioSnapshot: battle.Scenario{ID: "scenario-1"}, RequestedTicks: 25,
	}

	t.Run("success", func(t *testing.T) {
		db := &battleDB{execTag: pgconn.NewCommandTag("INSERT 0 1")}
		if err := NewPostgres(db).CreateBattle(context.Background(), params); err != nil {
			t.Fatalf("CreateBattle() returned an error: %v", err)
		}
		if len(db.execArgs) != 6 || db.execArgs[0] != params.ID || db.execArgs[4] != params.RequestedTicks {
			t.Fatalf("Exec arguments = %#v", db.execArgs)
		}
		var stored battle.Scenario
		if err := json.Unmarshal(db.execArgs[3].([]byte), &stored); err != nil || stored.ID != params.ScenarioID {
			t.Fatalf("stored scenario = %#v, error = %v", stored, err)
		}
	})

	for _, test := range []struct {
		name string
		err  error
		want error
	}{
		{name: "duplicate", err: &pgconn.PgError{Code: "23505"}, want: battleservice.ErrBattleAlreadyExists},
		{name: "database error", err: errDatabase, want: errDatabase},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := NewPostgres(&battleDB{execErr: test.err}).CreateBattle(context.Background(), params)
			if !errors.Is(err, test.want) {
				t.Fatalf("CreateBattle() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestGetBattleDecodesStoredRun(t *testing.T) {
	now := time.Now().UTC()
	started := now.Add(time.Second)
	completed := now.Add(2 * time.Second)
	errorCode := "none"
	errorMessage := ""
	winner := "northwind"
	scenario := battle.Scenario{ID: "scenario-1", RulesVersion: "1"}
	result := battle.Result{WinnerFactionID: &winner, Reason: "elimination", TicksRun: 4}
	scenarioJSON, _ := json.Marshal(scenario)
	resultJSON, _ := json.Marshal(result)
	stateJSON, _ := json.Marshal(battle.State{BattleID: "battle-1", Tick: 4})
	db := &battleDB{row: battleRow(func(destinations ...any) error {
		values := []any{
			"battle-1", "scenario-1", "digest", scenarioJSON, uint64(10), stateJSON, stateJSON, uint64(4),
			"completed", 2, resultJSON, &errorCode, &errorMessage, now, &started, &completed,
		}
		return assignBattleValues(destinations, values)
	})}

	run, err := NewPostgres(db).GetBattle(context.Background(), "battle-1")
	if err != nil {
		t.Fatalf("GetBattle() returned an error: %v", err)
	}
	if run.ID != "battle-1" || run.ScenarioSnapshot.ID != scenario.ID || run.Result == nil || run.Result.Reason != result.Reason {
		t.Fatalf("GetBattle() = %#v", run)
	}
}

func TestGetBattleMapsReadAndDecodeErrors(t *testing.T) {
	tests := []struct {
		name string
		row  pgx.Row
		want error
	}{
		{name: "not found", row: battleRow(func(...any) error { return pgx.ErrNoRows }), want: battleservice.ErrBattleNotFound},
		{name: "query", row: battleRow(func(...any) error { return errDatabase }), want: errDatabase},
		{name: "scenario JSON", row: storedBattleRow([]byte("{"), nil), want: errors.New("decode battle")},
		{name: "result JSON", row: storedBattleRow([]byte(`{"id":"scenario-1"}`), []byte("{")), want: errors.New("decode battle")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewPostgres(&battleDB{row: test.row}).GetBattle(context.Background(), "battle-1")
			if test.want == errDatabase || test.want == battleservice.ErrBattleNotFound {
				if !errors.Is(err, test.want) {
					t.Fatalf("GetBattle() error = %v, want %v", err, test.want)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.want.Error()) {
				t.Fatalf("GetBattle() error = %v, want text %q", err, test.want)
			}
		})
	}
}

func TestListBattleEventsDecodesRowsAndMapsErrors(t *testing.T) {
	now := time.Now().UTC()
	rows := &battleRows{scans: []battleRow{
		func(destinations ...any) error {
			return assignBattleValues(destinations, []any{2, uint64(3), uint64(4), "tick_completed", []byte(`{"damage":7}`), now})
		},
	}}
	events, err := NewPostgres(&battleDB{rows: rows}).ListBattleEvents(context.Background(), "battle-1", 2, 1, 50, false)
	if err != nil {
		t.Fatalf("ListBattleEvents() returned an error: %v", err)
	}
	want := []battleservice.StoredBattleEvent{{Attempt: 2, Sequence: 3, Tick: 4, Type: "tick_completed", Data: map[string]any{"damage": float64(7)}, CreatedAt: now}}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}

	tests := []struct {
		name string
		db   *battleDB
	}{
		{name: "query", db: &battleDB{queryErr: errDatabase}},
		{name: "scan", db: &battleDB{rows: &battleRows{scans: []battleRow{func(...any) error { return errDatabase }}}}},
		{name: "payload", db: &battleDB{rows: &battleRows{scans: []battleRow{func(destinations ...any) error {
			return assignBattleValues(destinations, []any{1, uint64(1), uint64(1), "event", []byte("{"), now})
		}}}}},
		{name: "iteration", db: &battleDB{rows: &battleRows{iterationErr: errDatabase}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewPostgres(test.db).ListBattleEvents(context.Background(), "battle-1", 1, 0, 50, true); err == nil {
				t.Fatal("ListBattleEvents() returned no error")
			}
		})
	}
}

func TestClaimBattleClaimsAndDecodesJob(t *testing.T) {
	scenario := battle.Scenario{ID: "scenario-1", RulesVersion: "1"}
	snapshot, _ := json.Marshal(scenario)
	db := &battleDB{execTag: pgconn.NewCommandTag("UPDATE 0"), row: battleRow(func(destinations ...any) error {
		return assignBattleValues(destinations, []any{"battle-1", 2, snapshot, uint64(30)})
	})}

	job, err := NewPostgres(db).ClaimBattle(context.Background(), "worker-1", time.Minute, 3)
	if err != nil {
		t.Fatalf("ClaimBattle() returned an error: %v", err)
	}
	if job.Lease != (worker.BattleLease{BattleID: "battle-1", Attempt: 2, Owner: "worker-1"}) || job.Scenario.ID != scenario.ID || job.RequestedTicks != 30 {
		t.Fatalf("ClaimBattle() = %#v", job)
	}
}

func TestClaimBattleMapsQueueErrors(t *testing.T) {
	tests := []struct {
		name string
		db   *battleDB
		want error
	}{
		{name: "fail exhausted", db: &battleDB{execErr: errDatabase}, want: errDatabase},
		{name: "empty", db: &battleDB{row: battleRow(func(...any) error { return pgx.ErrNoRows })}, want: worker.ErrNoBattleAvailable},
		{name: "claim", db: &battleDB{row: battleRow(func(...any) error { return errDatabase })}, want: errDatabase},
		{name: "scenario JSON", db: &battleDB{row: battleRow(func(destinations ...any) error {
			return assignBattleValues(destinations, []any{"battle-1", 1, []byte("{"), uint64(1)})
		})}, want: errDatabase},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewPostgres(test.db).ClaimBattle(context.Background(), "worker-1", time.Minute, 3)
			if test.name == "scenario JSON" {
				if err == nil || !strings.Contains(err.Error(), "decode claimed battle") {
					t.Fatalf("ClaimBattle() error = %v", err)
				}
			} else if !errors.Is(err, test.want) {
				t.Fatalf("ClaimBattle() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestLeaseMutations(t *testing.T) {
	lease := worker.BattleLease{BattleID: "battle-1", Attempt: 2, Owner: "worker-1"}
	transition := battle.Transition{Tick: 3, Events: []battle.Event{{Sequence: 1, Tick: 3, Type: "tick"}}}

	t.Run("heartbeat", func(t *testing.T) {
		for _, test := range leaseExecCases() {
			t.Run(test.name, func(t *testing.T) {
				err := NewPostgres(test.db()).HeartbeatBattle(context.Background(), lease, time.Minute)
				assertLeaseError(t, err, test.want)
			})
		}
	})

	t.Run("save tick", func(t *testing.T) {
		for _, test := range leaseRowCases() {
			t.Run(test.name, func(t *testing.T) {
				err := NewPostgres(test.db()).SaveTick(context.Background(), lease, transition, time.Minute)
				assertLeaseError(t, err, test.want)
			})
		}
	})

	t.Run("complete validates transition", func(t *testing.T) {
		if err := NewPostgres(&battleDB{}).CompleteBattle(context.Background(), lease, transition); err == nil {
			t.Fatal("CompleteBattle() accepted an unfinished transition")
		}
	})

	t.Run("complete", func(t *testing.T) {
		finished := transition
		finished.Finished = true
		finished.Result = &battle.Result{Reason: "tick_limit", TicksRun: transition.Tick}
		for _, test := range leaseRowCases() {
			t.Run(test.name, func(t *testing.T) {
				err := NewPostgres(test.db()).CompleteBattle(context.Background(), lease, finished)
				assertLeaseError(t, err, test.want)
			})
		}
	})

	for _, operation := range []struct {
		name string
		run  func(*Postgres) error
	}{
		{name: "fail", run: func(repository *Postgres) error {
			return repository.FailBattle(context.Background(), lease, "failed", "message")
		}},
		{name: "requeue", run: func(repository *Postgres) error {
			return repository.RequeueBattle(context.Background(), lease)
		}},
	} {
		t.Run(operation.name, func(t *testing.T) {
			for _, test := range leaseExecCases() {
				t.Run(test.name, func(t *testing.T) {
					assertLeaseError(t, operation.run(NewPostgres(test.db())), test.want)
				})
			}
		})
	}
}

type leaseCase struct {
	name string
	db   func() *battleDB
	want error
}

func leaseExecCases() []leaseCase {
	return []leaseCase{
		{name: "success", db: func() *battleDB { return &battleDB{execTag: pgconn.NewCommandTag("UPDATE 1")} }},
		{name: "database error", db: func() *battleDB { return &battleDB{execErr: errDatabase} }, want: errDatabase},
		{name: "lease lost", db: func() *battleDB { return &battleDB{execTag: pgconn.NewCommandTag("UPDATE 0")} }, want: worker.ErrBattleLeaseLost},
	}
}

func leaseRowCases() []leaseCase {
	return []leaseCase{
		{name: "success", db: func() *battleDB { return &battleDB{row: booleanBattleRow(true)} }},
		{name: "database error", db: func() *battleDB { return &battleDB{row: battleRow(func(...any) error { return errDatabase })} }, want: errDatabase},
		{name: "lease lost", db: func() *battleDB { return &battleDB{row: booleanBattleRow(false)} }, want: worker.ErrBattleLeaseLost},
	}
}

func assertLeaseError(t *testing.T, err, want error) {
	t.Helper()
	if want == nil && err != nil {
		t.Fatalf("operation returned an error: %v", err)
	}
	if want != nil && !errors.Is(err, want) {
		t.Fatalf("operation error = %v, want %v", err, want)
	}
}

type battleDB struct {
	row      pgx.Row
	rows     pgx.Rows
	queryErr error
	execTag  pgconn.CommandTag
	execErr  error
	execArgs []any
}

func (db *battleDB) Exec(_ context.Context, _ string, arguments ...any) (pgconn.CommandTag, error) {
	db.execArgs = arguments
	return db.execTag, db.execErr
}

func (db *battleDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return db.rows, db.queryErr
}

func (db *battleDB) QueryRow(context.Context, string, ...any) pgx.Row {
	if db.row == nil {
		return battleRow(func(...any) error { return pgx.ErrNoRows })
	}
	return db.row
}

type battleRow func(...any) error

func (row battleRow) Scan(destinations ...any) error { return row(destinations...) }

func booleanBattleRow(value bool) battleRow {
	return func(destinations ...any) error {
		*(destinations[0].(*bool)) = value
		return nil
	}
}

func storedBattleRow(snapshot, result []byte) battleRow {
	return func(destinations ...any) error {
		state, _ := json.Marshal(battle.State{BattleID: "battle-1"})
		return assignBattleValues(destinations, []any{
			"battle-1", "scenario-1", "digest", snapshot, uint64(10), state, state, uint64(0),
			"queued", 1, result, (*string)(nil), (*string)(nil), time.Now(), (*time.Time)(nil), (*time.Time)(nil),
		})
	}
}

func assignBattleValues(destinations, values []any) error {
	if len(destinations) != len(values) {
		return errors.New("unexpected scan destination count")
	}
	for index := range values {
		destination := reflect.ValueOf(destinations[index])
		value := reflect.ValueOf(values[index])
		if !value.IsValid() {
			destination.Elem().SetZero()
			continue
		}
		destination.Elem().Set(value)
	}
	return nil
}

type battleRows struct {
	scans        []battleRow
	index        int
	iterationErr error
}

func (rows *battleRows) Close()                                       {}
func (rows *battleRows) Err() error                                   { return rows.iterationErr }
func (rows *battleRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (rows *battleRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (rows *battleRows) Values() ([]any, error)                       { return nil, nil }
func (rows *battleRows) RawValues() [][]byte                          { return nil }
func (rows *battleRows) Conn() *pgx.Conn                              { return nil }
func (rows *battleRows) Next() bool {
	if rows.index >= len(rows.scans) {
		return false
	}
	rows.index++
	return true
}
func (rows *battleRows) Scan(destinations ...any) error {
	return rows.scans[rows.index-1](destinations...)
}
