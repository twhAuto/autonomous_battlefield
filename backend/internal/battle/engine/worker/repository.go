package worker

import (
	"context"
	"errors"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/game"
)

var (
	ErrNoBattleAvailable = errors.New("no battle available")
	ErrBattleLeaseLost   = errors.New("battle lease lost")
)

type BattleLease struct {
	BattleID string
	Attempt  int
	Owner    string
}

type BattleJob struct {
	Lease          BattleLease
	Scenario       game.Scenario
	RequestedTicks uint64
}

// Repository describes the durable queue operations required by the worker.
type Repository interface {
	ClaimBattle(context.Context, string, time.Duration, int) (*BattleJob, error)
	SaveTick(context.Context, BattleLease, game.Transition, time.Duration) error
	HeartbeatBattle(context.Context, BattleLease, time.Duration) error
	CompleteBattle(context.Context, BattleLease, game.Transition) error
	FailBattle(context.Context, BattleLease, string, string) error
	RequeueBattle(context.Context, BattleLease) error
}
