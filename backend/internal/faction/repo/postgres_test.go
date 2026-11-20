// Checks faction SQL reads and writes, row decoding, missing records, duplicate
// identifiers, and the transaction of an owner-limited create.

package repo

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/faction"
	factionservice "github.com/twhAuto/autonomous_battlefield/backend/internal/faction/service"
)

func TestGetFaction(t *testing.T) {
	db := &fakeDB{row: fakeRow{values: []any{"7f22e693-bacd-46bb-b942-64fa613cd388", "Northwind"}}}
	repo := NewPostgres(db)

	stored, err := repo.GetFaction(context.Background(), "7f22e693-bacd-46bb-b942-64fa613cd388")
	if err != nil {
		t.Fatalf("GetFaction() returned an error: %v", err)
	}
	want := &faction.Faction{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", Name: "Northwind"}
	if !reflect.DeepEqual(stored, want) {
		t.Fatalf("GetFaction() = %#v, want %#v", stored, want)
	}
}

func TestGetFactionReturnsNotFound(t *testing.T) {
	db := &fakeDB{row: fakeRow{err: pgx.ErrNoRows}}
	repo := NewPostgres(db)

	_, err := repo.GetFaction(context.Background(), "7f22e693-bacd-46bb-b942-64fa613cd388")
	if !errors.Is(err, factionservice.ErrFactionNotFound) {
		t.Fatalf("GetFaction() error = %v, want ErrFactionNotFound", err)
	}
}

func TestListFactions(t *testing.T) {
	db := &fakeDB{rows: &fakeRows{values: [][]any{
		{"7f22e693-bacd-46bb-b942-64fa613cd388", "Northwind"},
		{"4db8d152-1e04-4d10-8824-859c6ed72878", "Verdant Accord"},
	}, index: -1}}
	repo := NewPostgres(db)

	factions, err := repo.ListFactions(context.Background())
	if err != nil {
		t.Fatalf("ListFactions() returned an error: %v", err)
	}
	want := []faction.Faction{
		{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", Name: "Northwind"},
		{ID: "4db8d152-1e04-4d10-8824-859c6ed72878", Name: "Verdant Accord"},
	}
	if !reflect.DeepEqual(factions, want) {
		t.Fatalf("ListFactions() = %#v, want %#v", factions, want)
	}
	if !strings.Contains(db.querySQL, "ORDER BY created_at ASC, id ASC") {
		t.Fatalf("query = %q, want stable creation ordering", db.querySQL)
	}
}

func TestCreateFaction(t *testing.T) {
	db := &fakeDB{}
	repo := NewPostgres(db)
	stored := faction.Faction{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", Name: "Northwind", OwnerID: "player-7"}

	if err := repo.CreateFaction(context.Background(), stored, 0); err != nil {
		t.Fatalf("CreateFaction() returned an error: %v", err)
	}
	if db.began {
		t.Fatal("an unlimited create opened a transaction")
	}

	wantArguments := []any{stored.ID, stored.Name, stored.Doctrine, stored.Background, "player-7", stored.Emblem}
	if !reflect.DeepEqual(db.execArguments, wantArguments) {
		t.Fatalf("Exec arguments = %#v, want %#v", db.execArguments, wantArguments)
	}
}

func TestCreateFactionReturnsAlreadyExists(t *testing.T) {
	db := &fakeDB{execErr: &pgconn.PgError{Code: "23505"}}
	repo := NewPostgres(db)

	err := repo.CreateFaction(context.Background(), faction.Faction{
		ID:   "7f22e693-bacd-46bb-b942-64fa613cd388",
		Name: "Northwind",
	}, 0)
	if !errors.Is(err, factionservice.ErrFactionAlreadyExists) {
		t.Fatalf("CreateFaction() error = %v, want ErrFactionAlreadyExists", err)
	}
}

func TestCreateFactionWithOwnerLimit(t *testing.T) {
	stored := faction.Faction{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", Name: "Northwind", OwnerID: "player-7"}
	offline := errors.New("offline")
	for name, test := range map[string]struct {
		db                  *fakeDB
		want                error
		inserted, committed bool
	}{
		"admitted":      {db: &fakeDB{tx: &fakeTx{}}, inserted: true, committed: true},
		"limit reached": {db: &fakeDB{tx: &fakeTx{limited: true}}, want: factionservice.ErrFactionLimitReached},
		"reused ID":     {db: &fakeDB{tx: &fakeTx{insertErr: &pgconn.PgError{Code: "23505"}}}, want: factionservice.ErrFactionAlreadyExists, inserted: true},
		"begin fails":   {db: &fakeDB{beginErr: offline}, want: offline},
		"lock fails":    {db: &fakeDB{tx: &fakeTx{lockErr: offline}}, want: offline},
		"count fails":   {db: &fakeDB{tx: &fakeTx{countErr: offline}}, want: offline},
		"commit fails":  {db: &fakeDB{tx: &fakeTx{commitErr: offline}}, want: offline, inserted: true, committed: true},
	} {
		t.Run(name, func(t *testing.T) {
			err := NewPostgres(test.db).CreateFaction(context.Background(), stored, 1)
			if !errors.Is(err, test.want) || (test.want == nil) != (err == nil) {
				t.Fatalf("CreateFaction() error = %v, want %v", err, test.want)
			}
			if test.db.tx != nil {
				checkLimitedCreateTransaction(t, test.db.tx, test.inserted, test.committed)
			}
		})
	}
}

// checkLimitedCreateTransaction requires the owner lock first, an insert only when
// expected, and the deferred rollback that ends every transaction.
func checkLimitedCreateTransaction(t *testing.T, tx *fakeTx, inserted, committed bool) {
	t.Helper()
	if !strings.Contains(tx.statements[0], "pg_advisory_xact_lock") || (len(tx.statements) == 2) != inserted {
		t.Fatalf("statements = %q, want the owner lock first and an insert only when %v", tx.statements, inserted)
	}
	if tx.committed != committed || !tx.rolledBack {
		t.Fatalf("committed = %v, rolled back = %v, want committed %v and a deferred rollback", tx.committed, tx.rolledBack, committed)
	}
}

func TestDeleteFaction(t *testing.T) {
	id := "7f22e693-bacd-46bb-b942-64fa613cd388"
	db := &fakeDB{execTag: pgconn.NewCommandTag("DELETE 1")}
	if err := NewPostgres(db).DeleteFaction(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(db.execArguments, []any{id}) {
		t.Fatalf("arguments = %#v", db.execArguments)
	}

	db.execTag = pgconn.NewCommandTag("DELETE 0")
	if err := NewPostgres(db).DeleteFaction(context.Background(), id); !errors.Is(err, factionservice.ErrFactionNotFound) {
		t.Fatalf("error = %v, want ErrFactionNotFound", err)
	}
	db.execErr = errors.New("database unavailable")
	if err := NewPostgres(db).DeleteFaction(context.Background(), id); err == nil {
		t.Fatal("repository error was ignored")
	}
}

type fakeDB struct {
	tx            *fakeTx
	beginErr      error
	began         bool
	row           pgx.Row
	rows          pgx.Rows
	querySQL      string
	execArguments []any
	execTag       pgconn.CommandTag
	execErr       error
}

func (db *fakeDB) QueryRow(context.Context, string, ...any) pgx.Row {
	return db.row
}

func (db *fakeDB) Begin(context.Context) (pgx.Tx, error) {
	db.began = true
	if db.beginErr != nil {
		return nil, db.beginErr
	}
	return db.tx, nil
}

// fakeTx records an owner-limited create; the embedded nil pgx.Tx panics on any
// other method.
type fakeTx struct {
	pgx.Tx
	limited                                 bool
	lockErr, countErr, insertErr, commitErr error
	statements                              []string
	committed, rolledBack                   bool
}

func (tx *fakeTx) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	tx.statements = append(tx.statements, sql)
	if strings.Contains(sql, "pg_advisory_xact_lock") {
		return pgconn.NewCommandTag("SELECT 1"), tx.lockErr
	}
	return pgconn.NewCommandTag("INSERT 0 1"), tx.insertErr
}

func (tx *fakeTx) QueryRow(context.Context, string, ...any) pgx.Row {
	return boolRow{value: tx.limited, err: tx.countErr}
}

func (tx *fakeTx) Commit(context.Context) error {
	tx.committed = true
	return tx.commitErr
}

func (tx *fakeTx) Rollback(context.Context) error {
	tx.rolledBack = true
	return nil
}

type boolRow struct {
	value bool
	err   error
}

func (r boolRow) Scan(destinations ...any) error {
	if r.err != nil {
		return r.err
	}
	*destinations[0].(*bool) = r.value
	return nil
}

func (db *fakeDB) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	db.querySQL = sql
	return db.rows, nil
}

func (db *fakeDB) Exec(_ context.Context, _ string, arguments ...any) (pgconn.CommandTag, error) {
	db.execArguments = arguments
	if db.execTag.String() == "" {
		db.execTag = pgconn.NewCommandTag("INSERT 0 1")
	}
	return db.execTag, db.execErr
}

type fakeRow struct {
	values []any
	err    error
}

func (r fakeRow) Scan(destinations ...any) error {
	if r.err != nil {
		return r.err
	}
	for i, value := range r.values {
		pointer, ok := destinations[i].(*string)
		if !ok {
			return errors.New("fakeRow only supports string destinations")
		}
		*pointer = value.(string)
	}
	return nil
}

type fakeRows struct {
	values [][]any
	index  int
	err    error
}

func (r *fakeRows) Close()                                       {}
func (r *fakeRows) Err() error                                   { return r.err }
func (r *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) Values() ([]any, error)                       { return r.values[r.index], nil }
func (r *fakeRows) RawValues() [][]byte                          { return nil }
func (r *fakeRows) Conn() *pgx.Conn                              { return nil }

func (r *fakeRows) Next() bool {
	r.index++
	return r.index < len(r.values)
}

func (r *fakeRows) Scan(destinations ...any) error {
	for i, value := range r.values[r.index] {
		pointer, ok := destinations[i].(*string)
		if !ok {
			return errors.New("fakeRows only supports string destinations")
		}
		*pointer = value.(string)
	}
	return nil
}

func TestUpdateFaction(t *testing.T) {
	stored := faction.Faction{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", Name: "A", Doctrine: "Scout"}
	db := &fakeDB{execTag: pgconn.NewCommandTag("UPDATE 1")}
	if err := NewPostgres(db).UpdateFaction(context.Background(), stored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(db.execArguments, []any{stored.ID, stored.Name, stored.Doctrine, stored.Background, stored.Emblem}) {
		t.Fatal(db.execArguments)
	}
	db.execTag = pgconn.NewCommandTag("UPDATE 0")
	if err := NewPostgres(db).UpdateFaction(context.Background(), stored); !errors.Is(err, factionservice.ErrFactionNotFound) {
		t.Fatal(err)
	}
	db.execErr = errors.New("offline")
	if err := NewPostgres(db).UpdateFaction(context.Background(), stored); err == nil {
		t.Fatal("ignored database failure")
	}
}
