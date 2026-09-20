// Persists model-access policies and translates optimistic-write conflicts.
package admin

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/twh09/autonomous_battlefield/backend/internal/postgres"
)

type Postgres struct{ DB postgres.DBTX }

func (p *Postgres) Read(ctx context.Context) (Settings, error) {
	var s Settings
	var raw []byte
	err := p.DB.QueryRow(ctx, "SELECT version,policy FROM model_access_policy WHERE id").Scan(&s.Version, &raw)
	if err != nil {
		return s, err
	}
	err = json.Unmarshal(raw, &s.Policy)
	return s, err
}

func (p *Postgres) Save(ctx context.Context, s Settings, actorID string) (Settings, error) {
	raw, err := json.Marshal(s.Policy)
	if err != nil {
		return s, err
	}
	err = p.DB.QueryRow(ctx, "UPDATE model_access_policy SET policy=$1,version=version+1,updated_by=$2,updated_at=now() WHERE id AND version=$3 RETURNING version", raw, actorID, s.Version).Scan(&s.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, ErrConflict
	}
	return s, err
}
