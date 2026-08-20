package service

import (
	"context"

	"github.com/twh09/autonomous_battlefield/backend/internal/faction"
)

// Repository describes the battle persistence required by the API-facing service.
type Repository interface {
	CreateBattle(context.Context, CreateBattleParams) error
	GetBattle(context.Context, string) (*BattleRun, error)
	ListBattleEvents(context.Context, string, int, uint64, int) ([]StoredBattleEvent, error)
}

// factionRepository is the narrower faction view required to create a battle.
type factionRepository interface {
	GetFaction(context.Context, string) (*faction.Faction, error)
}
