package members

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
)

// Reconcile uses a transaction advisory lock across API replicas. Repeatable-read
// detects edits during the remote call: a stale response can never activate a
// newer grant. A rolled-back/ambiguous update is repaired by sending the full list.
func (s *Store) Reconcile(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return err
	}
	defer rollback(tx)
	var locked bool
	if err = tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock(741928310)").Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return nil
	}
	var version int64
	var due bool
	if err = tx.QueryRow(ctx, "SELECT version,initialized AND next_attempt<=now() FROM player_membership_sync WHERE id").Scan(&version, &due); err != nil {
		return err
	}
	if !due {
		return nil
	}
	rows, err := tx.Query(ctx, "SELECT email FROM player_members WHERE desired_access ORDER BY email")
	if err != nil {
		return err
	}
	emails := []string{}
	for rows.Next() {
		var email string
		if err = rows.Scan(&email); err != nil {
			rows.Close()
			return err
		}
		if !s.protected(email) {
			emails = append(emails, email)
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	if err = s.Writer.Replace(ctx, emails); err != nil {
		// Persist backoff while still holding the worker lock. Concurrent desired-state
		// edits may cause a serialization failure; their newer work remains due.
		_, saveErr := tx.Exec(ctx, `UPDATE player_membership_sync SET attempts=LEAST(attempts+1,7),
    next_attempt=now()+make_interval(secs => LEAST(300,5*power(2,LEAST(attempts,6)))::double precision),last_error=$1 WHERE id`, err.Error())
		if saveErr != nil {
			return saveErr
		}
		if saveErr = tx.Commit(ctx); saveErr != nil {
			return saveErr
		}
		return err
	}
	// Use the same state-row-before-members lock order as Change. Locking the
	// member rows first would deadlock with a concurrent removal holding state.
	if _, err = tx.Exec(ctx, "UPDATE player_membership_sync SET synced_version=$1,attempts=0,last_error='',next_attempt=now()+interval '60 seconds' WHERE id", version); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "UPDATE player_members SET synced_version=version WHERE synced_version<>version"); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Run stops on process cancellation. Requests and transactions have their own
// deadlines, and each API replica may safely run this loop.
func (s *Store) Run(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if err := s.Reconcile(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("player_members_sync_failed", "reason", fmt.Sprintf("%T", err))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
