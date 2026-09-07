// Listens for PostgreSQL notifications on a dedicated pooled connection and forwards
// payloads to a wakeup callback.

package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ListenForNotifications reserves one pool connection and delivers PostgreSQL
// LISTEN/NOTIFY payloads until the context is cancelled. Callers should treat
// notifications as advisory and query durable state after every wake-up.
func ListenForNotifications(ctx context.Context, pool *pgxpool.Pool, channel string, notify func(string)) error {
	if pool == nil || channel == "" || notify == nil {
		return fmt.Errorf("notification listener requires a pool, channel, and callback")
	}
	connection, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire PostgreSQL notification connection: %w", err)
	}
	defer connection.Release()

	if _, err := connection.Exec(ctx, "LISTEN "+pgx.Identifier{channel}.Sanitize()); err != nil {
		return fmt.Errorf("listen for PostgreSQL notifications: %w", err)
	}
	for {
		notification, err := connection.Conn().WaitForNotification(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("wait for PostgreSQL notification: %w", err)
		}
		notify(notification.Payload)
	}
}
