package members

import (
	"context"
	"log/slog"
	"time"
)

// removedRetention is how long a removed member's row, and with it the email, is kept
// after the removal. Only removals Cloudflare has confirmed expire.
const removedRetention = 7 * 24 * time.Hour

// purgeInterval spaces purges in the reconciler loop. Every API replica purges, and a
// purge that finds nothing to delete is harmless.
const purgeInterval = time.Hour

// Purge deletes removed members whose confirmed removal is older than removedRetention
// and returns how many it deleted. Their audit rows stay, without the member link. It
// locks the singleton desired-state row first, as Change does, so a concurrent grant
// either restores the row before the purge or creates a fresh one after it.
func (s *Store) Purge(ctx context.Context) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer rollback(tx)
	if _, err = tx.Exec(ctx, "SELECT id FROM player_membership_sync WHERE id FOR UPDATE"); err != nil {
		return 0, err
	}
	result, err := tx.Exec(ctx, `DELETE FROM player_members WHERE NOT desired_access AND synced_version=version
 AND updated_at < now()-make_interval(secs => $1)`, removedRetention.Seconds())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), tx.Commit(ctx)
}

// expire purges expired members and logs the outcome by count or fixed reason, never
// by email.
func (s *Store) expire(ctx context.Context) {
	count, err := s.Purge(ctx)
	switch {
	case err != nil && ctx.Err() == nil:
		slog.Warn("player_members_purge_failed", "reason", failureReason(err))
	case count > 0:
		slog.Info("player_members_purged", "count", count)
	}
}
