// Defines the faction persistence operations needed by the service without depending on a
// database driver.

package service

import (
	"context"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/faction"
)

// Repository describes the faction persistence required by the service.
type Repository interface {
	GetFaction(context.Context, string) (*faction.Faction, error)
	ListFactions(context.Context) ([]faction.Faction, error)
	// CreateFaction stores a new faction. A positive ownerLimit refuses it with
	// ErrFactionLimitReached while its owner already owns that many factions; the
	// count and insert are serialized per owner, so concurrent creates cannot both pass.
	CreateFaction(ctx context.Context, stored faction.Faction, ownerLimit int) error
	UpdateFaction(context.Context, faction.Faction) error
	DeleteFaction(context.Context, string) error
}
