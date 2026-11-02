// Defines the persistence operations required by battle use cases, independently of the
// PostgreSQL implementation.

package service

import (
	"context"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/faction"
)

// Repository describes the battle persistence required by the API-facing service.
type Repository interface {
	ListBattles(context.Context, BattleHistoryQuery) ([]BattleSummary, error)
	CreateBattle(context.Context, CreateBattleParams) error
	// BattleAllowance reads an owner's limited allowance: the Tester values when tester
	// is set, otherwise the Player values.
	BattleAllowance(ctx context.Context, owner string, tester bool) (Allowance, error)
	GetBattle(context.Context, string) (*BattleRun, error)
	StopBattle(context.Context, string) error
	SetBattlePaused(context.Context, string, bool) error
	ListBattleEvents(context.Context, string, int, uint64, int, bool) ([]StoredBattleEvent, error)
	ListBattleDecisions(context.Context, string, int) ([]ai.DecisionRecord, error)
	GetDecision(context.Context, string) (ai.DecisionRecord, error)
	GetReplayFrame(context.Context, string, int, uint64) (StoredReplayFrame, error)
}

// factionRepository is the narrower faction view required to create a battle.
type factionRepository interface {
	GetFaction(context.Context, string) (*faction.Faction, error)
}
