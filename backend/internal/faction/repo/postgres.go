// Implements faction profile reads and writes in PostgreSQL, mapping database outcomes to
// service errors.

package repo

import (
	"context"
	"encoding/json"
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

func NewPostgres(db postgres.DBTX) *Postgres {
	return &Postgres{db: db}
}

const selectFaction = `SELECT id, name, COALESCE(ruleset_id, ''), blueprint, doctrine, background, owner_id, emblem FROM factions`

// scanFaction decodes one selectFaction row; older factions also carry a blueprint.
func scanFaction(row pgx.Row) (faction.Faction, error) {
	var stored faction.Faction
	var blueprint []byte
	if err := row.Scan(&stored.ID, &stored.Name, &stored.RulesetID, &blueprint, &stored.Doctrine, &stored.Background, &stored.OwnerID, &stored.Emblem); err != nil {
		return faction.Faction{}, err
	}
	if len(blueprint) > 0 {
		if err := json.Unmarshal(blueprint, &stored.Blueprint); err != nil {
			return faction.Faction{}, fmt.Errorf("decode faction %q blueprint: %w", stored.ID, err)
		}
	}
	return stored, nil
}

func (r *Postgres) GetFaction(ctx context.Context, id string) (*faction.Faction, error) {
	stored, err := scanFaction(r.db.QueryRow(ctx, selectFaction+` WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, factionservice.ErrFactionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get faction %q: %w", id, err)
	}
	return &stored, nil
}

func (r *Postgres) ListFactions(ctx context.Context) ([]faction.Faction, error) {
	rows, err := r.db.Query(ctx, selectFaction+` ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list factions: %w", err)
	}
	defer rows.Close()

	factions := make([]faction.Faction, 0)
	for rows.Next() {
		stored, err := scanFaction(rows)
		if err != nil {
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
        INSERT INTO factions (id, name, doctrine, background, owner_id, emblem) VALUES ($1, $2, $3, $4, $5, COALESCE(NULLIF($6, ''), 'star'))
    `, stored.ID, stored.Name, stored.Doctrine, stored.Background, stored.OwnerID, stored.Emblem)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return fmt.Errorf("%w: %q", factionservice.ErrFactionAlreadyExists, stored.ID)
		}
		return fmt.Errorf("create faction %q: %w", stored.ID, err)
	}

	return nil
}

func (r *Postgres) DeleteFaction(ctx context.Context, id string) error {
	tag, err := r.db.Exec(ctx, `DELETE FROM factions WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete faction %q: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return factionservice.ErrFactionNotFound
	}
	return nil
}

func (r *Postgres) UpdateFaction(ctx context.Context, stored faction.Faction) error {
	tag, err := r.db.Exec(ctx, `UPDATE factions SET name=$2, doctrine=$3, background=$4, emblem=COALESCE(NULLIF($5, ''), emblem) WHERE id=$1`, stored.ID, stored.Name, stored.Doctrine, stored.Background, stored.Emblem)
	if err != nil {
		return fmt.Errorf("update faction: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return factionservice.ErrFactionNotFound
	}
	return nil
}
