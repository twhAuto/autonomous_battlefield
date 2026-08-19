package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/twh09/autonomous_battlefield/backend/internal/service/entity"
)

var ErrFactionNotFound = errors.New("faction not found")

type DBTX interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type FactionRepository interface {
	GetFaction(ctx context.Context, id string) (*entity.FactionEntity, error)
	CreateFaction(ctx context.Context, faction *entity.FactionEntity) error
}

type factionRepo struct {
	db DBTX
}

func (r *factionRepo) GetFaction(ctx context.Context, id string) (*entity.FactionEntity, error) {
	var faction entity.FactionEntity
	err := r.db.QueryRow(ctx, `
		SELECT id, name
		FROM factions
		WHERE id = $1
	`, id).Scan(&faction.ID, &faction.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrFactionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get faction %q: %w", id, err)
	}

	return &faction, nil
}

func (r *factionRepo) CreateFaction(ctx context.Context, faction *entity.FactionEntity) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO factions (id, name)
		VALUES ($1, $2)
	`, faction.ID, faction.Name)
	if err != nil {
		return fmt.Errorf("create faction %q: %w", faction.ID, err)
	}

	return nil
}

func NewFactionRepo(db DBTX) FactionRepository {
	return &factionRepo{db: db}
}
