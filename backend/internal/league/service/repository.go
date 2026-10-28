// Defines the persistence and battle operations league use cases require, independently
// of the PostgreSQL and battle-service implementations.

package service

import (
	"context"

	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/faction"
	"github.com/twh09/autonomous_battlefield/backend/internal/league"
)

// Repository stores leagues. Fixtures carry their current battle's outcome.
type Repository interface {
	CreateLeague(context.Context, league.League) error
	GetLeague(context.Context, string) (*league.League, error)
	ListLeagues(context.Context, int) ([]league.Summary, error)
	// CancelLeague cancels a running league and its unfinished fixtures, serialized with
	// scheduling. It reports whether this call cancelled the league, and returns the
	// league's battles still queued or running, also on a repeated cancellation.
	CancelLeague(context.Context, string) (bool, []string, error)
	// Schedule runs fn while holding the scheduler lock, and reports false without
	// running it when another replica holds the lock.
	Schedule(context.Context, func(context.Context, SchedulerStore) error) (bool, error)
}

// SchedulerStore is the scheduler's view of storage inside one locked pass.
type SchedulerStore interface {
	RunningLeagues(context.Context) ([]league.League, error)
	SaveFixture(context.Context, league.Fixture) error
	AddFixtures(context.Context, string, []league.Fixture) error
	CompleteLeague(context.Context, string, int) error
	// EngineBusy reports whether any battle is queued or running.
	EngineBusy(context.Context) (bool, error)
	// Battle describes an existing battle's origin, or returns nil when there is none.
	Battle(context.Context, string) (*BattleProvenance, error)
	// StrandedBattles lists battles of cancelled leagues that are still queued or running.
	StrandedBattles(context.Context) ([]StrandedBattle, error)
}

// BattleProvenance is what the scheduler checks before adopting an existing battle.
type BattleProvenance struct {
	OwnerID              string
	DecisionBudgetExempt bool
	ScenarioID           string
	FactionIDs           []string
}

type StrandedBattle struct {
	LeagueID string
	BattleID string
	OwnerID  string
}

// Battles is the battle service as leagues use it.
type Battles interface {
	ValidateBattle(context.Context, battleservice.CreateBattleInput) error
	CreateBattle(context.Context, battleservice.CreateBattleInput) (*battleservice.BattleRun, error)
	StopBattle(context.Context, string) (*battleservice.BattleRun, error)
}

// Factions reads entrant faction names at creation.
type Factions interface {
	GetFaction(context.Context, string) (*faction.Faction, error)
}
