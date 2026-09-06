package worker

import (
	"context"
	"time"
)

const progressLogInterval = 15 * time.Second

type battleStartedKey struct{}

// progressLog emits the first update immediately and then at most once per
// interval. Each battle or decision window owns its own limiter.
type progressLog struct{ next time.Time }

func (p *progressLog) due(now time.Time) bool {
	if now.Before(p.next) {
		return false
	}
	p.next = now.Add(progressLogInterval)
	return true
}

func elapsedBattleMilliseconds(ctx context.Context) int64 {
	started, ok := ctx.Value(battleStartedKey{}).(time.Time)
	if !ok {
		return 0
	}
	return time.Since(started).Milliseconds()
}
