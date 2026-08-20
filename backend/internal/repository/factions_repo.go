package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/twh09/autonomous_battlefield/backend/internal/database"
	"github.com/twh09/autonomous_battlefield/backend/internal/service/entity"
)

var (
	ErrFactionNotFound      = errors.New("faction not found")
	ErrFactionAlreadyExists = errors.New("faction already exists")
)

type FactionRepository interface {
	GetFaction(ctx context.Context, id string) (*entity.FactionEntity, error)
	ListFactions(ctx context.Context) ([]entity.FactionEntity, error)
	CreateFaction(ctx context.Context, faction *entity.FactionEntity) error
}

type factionRepo struct {
	db database.DBTX
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

func (r *factionRepo) ListFactions(ctx context.Context) ([]entity.FactionEntity, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, name
		FROM factions
		ORDER BY created_at ASC, id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list factions: %w", err)
	}
	defer rows.Close()

	factions := make([]entity.FactionEntity, 0)
	for rows.Next() {
		var faction entity.FactionEntity
		if err := rows.Scan(&faction.ID, &faction.Name); err != nil {
			return nil, fmt.Errorf("scan faction: %w", err)
		}
		factions = append(factions, faction)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate factions: %w", err)
	}

	return factions, nil
}

func (r *factionRepo) CreateFaction(ctx context.Context, faction *entity.FactionEntity) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO factions (id, name)
		VALUES ($1, $2)
	`, faction.ID, faction.Name)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return fmt.Errorf("%w: %q", ErrFactionAlreadyExists, faction.ID)
		}
		return fmt.Errorf("create faction %q: %w", faction.ID, err)
	}

	return nil
}

func NewFactionRepo(db database.DBTX) FactionRepository {
	return &factionRepo{db: db}
}
