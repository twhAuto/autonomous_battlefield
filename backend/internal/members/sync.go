package members

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
	version, due, err := syncWork(ctx, tx)
	if err != nil {
		return err
	}
	if !due {
		return nil
	}
	emails, err := s.desiredEmails(ctx, tx)
	if err != nil {
		return err
	}
	if err = s.Writer.Replace(ctx, emails); err != nil {
		return recordSyncFailure(ctx, tx, err)
	}
	return confirmSync(ctx, tx, version)
}

func syncWork(ctx context.Context, tx pgx.Tx) (int64, bool, error) {
	var locked bool
	if err := tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock(741928310)").Scan(&locked); err != nil || !locked {
		return 0, false, err
	}
	var version int64
	var due bool
	err := tx.QueryRow(ctx, "SELECT version,initialized AND next_attempt<=now() FROM player_membership_sync WHERE id").Scan(&version, &due)
	return version, due, err
}

func (s *Store) desiredEmails(ctx context.Context, tx pgx.Tx) ([]string, error) {
	rows, err := tx.Query(ctx, "SELECT email FROM player_members WHERE desired_access ORDER BY email")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	emails := []string{}
	for rows.Next() {
		var email string
		if err = rows.Scan(&email); err != nil {
			return nil, err
		}
		if !s.protected(email) {
			emails = append(emails, email)
		}
	}
	return emails, rows.Err()
}

func recordSyncFailure(ctx context.Context, tx pgx.Tx, syncErr error) error {
	// Persist backoff while still holding the worker lock. Concurrent desired-state
	// edits may cause a serialization failure; their newer work remains due.
	_, err := tx.Exec(ctx, `UPDATE player_membership_sync SET attempts=LEAST(attempts+1,7),
    next_attempt=now()+make_interval(secs => LEAST(300,5*power(2,LEAST(attempts,6)))::double precision),last_error=$1 WHERE id`, syncErr.Error())
	if err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	return syncErr
}

func confirmSync(ctx context.Context, tx pgx.Tx, version int64) error {
	// Use the same state-row-before-members lock order as Change. Locking the
	// member rows first would deadlock with a concurrent removal holding state.
	if _, err := tx.Exec(ctx, "UPDATE player_membership_sync SET synced_version=$1,attempts=0,last_error='',next_attempt=now()+interval '60 seconds' WHERE id", version); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "UPDATE player_members SET synced_version=version WHERE synced_version<>version"); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Start imports the configured Players, then runs the group reconciler until the
// returned stop function is called. stop waits for the reconciler to exit.
func (s *Store) Start(ctx context.Context) (stop func(), err error) {
	initCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := s.Initialize(initCtx); err != nil {
		return nil, err
	}
	syncCtx, stopSync := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); s.Run(syncCtx) }()
	return func() { stopSync(); <-done }, nil
}

// Run stops on process cancellation. Requests and transactions have their own
// deadlines, and each API replica may safely run this loop. It also purges expired
// removed members, first on start and then every purgeInterval.
func (s *Store) Run(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	var purged time.Time
	for {
		if ctx.Err() != nil {
			return
		}
		if err := s.Reconcile(ctx); err != nil && ctx.Err() == nil {
			logSyncFailure(err)
		}
		if time.Since(purged) >= purgeInterval {
			purged = time.Now()
			s.expire(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func logSyncFailure(err error) {
	var remote *remoteError
	var database *pgconn.PgError
	switch {
	case errors.As(err, &remote):
		slog.Warn("player_members_sync_failed", "reason", remote.category, "http_status", remote.status, "detail", remote.Error())
	case errors.As(err, &database) && database.Code == "40001":
		slog.Debug("player_members_sync_retry", "reason", "concurrent_membership_change")
	case errors.Is(err, context.DeadlineExceeded):
		slog.Warn("player_members_sync_failed", "reason", "deadline_exceeded")
	default:
		slog.Warn("player_members_sync_failed", "reason", "database_unavailable")
	}
}
