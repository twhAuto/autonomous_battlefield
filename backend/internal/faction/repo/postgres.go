package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/twh09/autonomous_battlefield/backend/internal/faction"
	factionservice "github.com/twh09/autonomous_battlefield/backend/internal/faction/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/postgres"
)

type Postgres struct {
	db postgres.DBTX
}

func (r *Postgres) GetFaction(ctx context.Context, id string) (*faction.Faction, error) {
	var stored faction.Faction
	err := r.db.QueryRow(ctx, `
		SELECT id, name
		FROM factions
		WHERE id = $1
	`, id).Scan(&stored.ID, &stored.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, factionservice.ErrFactionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get faction %q: %w", id, err)
	}

	return &stored, nil
}

func (r *Postgres) ListFactions(ctx context.Context) ([]faction.Faction, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, name
		FROM factions
		ORDER BY created_at ASC, id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list factions: %w", err)
	}
	defer rows.Close()

	factions := make([]faction.Faction, 0)
	for rows.Next() {
		var stored faction.Faction
		if err := rows.Scan(&stored.ID, &stored.Name); err != nil {
			return nil, fmt.Errorf("scan faction: %w", err)
		}
		factions = append(factions, stored)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate factions: %w", err)
	}

	return factions, nil
}

func (r *Postgres) CreateFaction(ctx context.Context, stored faction.Faction) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO factions (id, name)
		VALUES ($1, $2)
	`, stored.ID, stored.Name)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return fmt.Errorf("%w: %q", factionservice.ErrFactionAlreadyExists, stored.ID)
		}
		return fmt.Errorf("create faction %q: %w", stored.ID, err)
	}

	return nil
}

func NewPostgres(db postgres.DBTX) *Postgres {
	return &Postgres{db: db}
}
