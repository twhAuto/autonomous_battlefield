package repository

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/twh09/autonomous_battlefield/backend/internal/service/entity"
)

func TestGetFaction(t *testing.T) {
	db := &fakeDB{row: fakeRow{values: []any{"7f22e693-bacd-46bb-b942-64fa613cd388", "Northwind"}}}
	repo := NewFactionRepo(db)

	faction, err := repo.GetFaction(context.Background(), "7f22e693-bacd-46bb-b942-64fa613cd388")
	if err != nil {
		t.Fatalf("GetFaction() returned an error: %v", err)
	}
	want := &entity.FactionEntity{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", Name: "Northwind"}
	if !reflect.DeepEqual(faction, want) {
		t.Fatalf("GetFaction() = %#v, want %#v", faction, want)
	}
}

func TestGetFactionReturnsNotFound(t *testing.T) {
	db := &fakeDB{row: fakeRow{err: pgx.ErrNoRows}}
	repo := NewFactionRepo(db)

	_, err := repo.GetFaction(context.Background(), "7f22e693-bacd-46bb-b942-64fa613cd388")
	if !errors.Is(err, ErrFactionNotFound) {
		t.Fatalf("GetFaction() error = %v, want ErrFactionNotFound", err)
	}
}

func TestCreateFaction(t *testing.T) {
	db := &fakeDB{}
	repo := NewFactionRepo(db)
	faction := &entity.FactionEntity{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", Name: "Northwind"}

	if err := repo.CreateFaction(context.Background(), faction); err != nil {
		t.Fatalf("CreateFaction() returned an error: %v", err)
	}

	wantArguments := []any{faction.ID, faction.Name}
	if !reflect.DeepEqual(db.execArguments, wantArguments) {
		t.Fatalf("Exec arguments = %#v, want %#v", db.execArguments, wantArguments)
	}
}

type fakeDB struct {
	row           pgx.Row
	execArguments []any
}

func (db *fakeDB) QueryRow(context.Context, string, ...any) pgx.Row {
	return db.row
}

func (db *fakeDB) Exec(_ context.Context, _ string, arguments ...any) (pgconn.CommandTag, error) {
	db.execArguments = arguments
	return pgconn.NewCommandTag("INSERT 0 1"), nil
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
