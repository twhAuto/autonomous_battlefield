// Defines the persistence and battle operations league use cases require, independently
// of the PostgreSQL and battle-service implementations.

package service

import (
	"context"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai"
	battleservice "github.com/twhAuto/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/faction"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/league"
)

// Repository stores leagues. Fixtures carry their current battle's outcome.
type Repository interface {
	CreateLeague(context.Context, league.League) error
	GetLeague(context.Context, string) (*league.League, error)
	ListLeagues(context.Context, ListQuery) ([]league.Summary, error)
	// CancelLeague cancels a running league and its unfinished fixtures, serialized with
	// scheduling. It reports whether this call cancelled the league, and returns the
	// league's battles still queued or running, also on a repeated cancellation.
	CancelLeague(context.Context, string) (bool, []string, error)
	// Schedule runs fn while holding the scheduler lock, and reports false without
	// running it when another replica holds the lock.
	Schedule(context.Context, func(context.Context, SchedulerStore) error) (bool, error)
	// Exclusive runs fn while holding the scheduler lock, waiting for a pass in progress
	// to finish, as CancelLeague does.
	Exclusive(context.Context, func(context.Context, SchedulerStore) error) error
	// CLISlots reads runtime_policy.cli_concurrency.
	CLISlots(context.Context) (int, error)
}

// SchedulerStore is storage inside one locked transaction: a scheduling pass or a
// disqualification.
type SchedulerStore interface {
	RunningLeagues(context.Context) ([]league.League, error)
	// League reads one league with its fixtures' battle outcomes.
	League(context.Context, string) (*league.League, error)
	// Disqualify records an entrant's disqualification; At is the time of the write.
	Disqualify(context.Context, string, int, league.Disqualification) error
	SaveFixture(context.Context, league.Fixture) error
	AddFixtures(context.Context, string, []league.Fixture) error
	CompleteLeague(context.Context, string, int) error
	// Capacity counts battles of any owner that are queued or running.
	Capacity(context.Context) (Capacity, error)
	// Battle describes an existing battle's origin, or returns nil when there is none.
	Battle(context.Context, string) (*BattleProvenance, error)
	// StrandedBattles lists battles still queued or running in cancelled leagues or
	// forfeited fixtures.
	StrandedBattles(context.Context) ([]StrandedBattle, error)
}

// Capacity is what is in use when a pass starts. Unfinished battles fill engine slots;
// their CLI model sides share the CLI execution slots.
type Capacity struct {
	Unfinished int
	CLISides   int
	CLISlots   int
}

// fits reports whether a battle with cliSides CLI model sides can start now. A battle
// that needs more CLI slots than exist still starts once no CLI side is running, so a
// league never stalls.
func (c Capacity) fits(battleSlots, cliSides int) bool {
	return c.Unfinished < battleSlots && (cliSides == 0 || c.CLISides == 0 || c.CLISides+cliSides <= c.CLISlots)
}

// Profiles looks up model profiles by ID.
type Profiles interface {
	Get(string) (ai.ModelProfile, bool)
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
