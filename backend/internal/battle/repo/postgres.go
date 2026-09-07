// Constructs the PostgreSQL adapter shared by battle services and workers; SQL operations
// are split across sibling files.

package repo

import (
	"github.com/twh09/autonomous_battlefield/backend/internal/postgres"
)

type Postgres struct {
	db postgres.DBTX
}

func NewPostgres(db postgres.DBTX) *Postgres {
	return &Postgres{db: db}
}
