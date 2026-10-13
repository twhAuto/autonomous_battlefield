// Defines the persistence contract the simulation worker needs; the job and lease
// types it uses live in battle/queue.

package worker

import (
	"context"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/battle/queue"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
)

// Repository describes the durable queue operations required by the worker.
type Repository interface {
	ExpireBattles(context.Context) error
	FailExhaustedBattles(context.Context, int) error
	ClaimBattle(context.Context, string, time.Duration, int) (*queue.Job, error)
	SaveTick(context.Context, queue.Lease, game.Transition, time.Duration) error
	HeartbeatBattle(context.Context, queue.Lease, time.Duration) error
	CompleteBattle(context.Context, queue.Lease, game.Transition) error
	FailBattle(context.Context, queue.Lease, string, string) error
	RequeueBattle(context.Context, queue.Lease) error
}

// pauseRepository is optional so in-memory repositories and existing adapters
// remain valid. Durable production repositories implement it to support the
// operator pause control without surrendering the battle lease.
type pauseRepository interface {
	BattlePaused(context.Context, queue.Lease) (bool, error)
}
