// Defines the persistence contract the simulation worker needs; the job and lease
// types it uses live in battle/queue.

package worker

import (
	"context"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/battle/queue"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
)

// Repository describes the durable queue operations required by the worker.
type Repository interface {
	BattlePaused(context.Context, queue.Lease) (bool, error)
	ExpireBattles(context.Context) error
	FailExhaustedBattles(context.Context, int) error
	ClaimBattle(context.Context, string, time.Duration, int) (*queue.Job, error)
	SaveTick(context.Context, queue.Lease, game.Transition, time.Duration) error
	HeartbeatBattle(context.Context, queue.Lease, time.Duration) error
	CompleteBattle(context.Context, queue.Lease, game.Transition) error
	FailBattle(context.Context, queue.Lease, string, string) error
	RequeueBattle(context.Context, queue.Lease) error
}

// DecisionRepository is the decision persistence the engine needs: the shared contract
// plus a cheap count of a window's unresolved decisions for polling while models work.
type DecisionRepository interface {
	ai.DecisionRepository
	CountPendingWindowDecisions(context.Context, string, int, uint64) (int, error)
}
