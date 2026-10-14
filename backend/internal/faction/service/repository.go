// Defines the faction persistence operations needed by the service without depending on a
// database driver.

package service

import (
	"context"

	"github.com/twh09/autonomous_battlefield/backend/internal/faction"
)

// Repository describes the faction persistence required by the service.
type Repository interface {
	GetFaction(context.Context, string) (*faction.Faction, error)
	ListFactions(context.Context) ([]faction.Faction, error)
	CreateFaction(context.Context, faction.Faction) error
	UpdateFaction(context.Context, faction.Faction) error
	DeleteFaction(context.Context, string) error
}
