// Checks the notification listener delivers payloads and stops cleanly on cancellation.

package postgres

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestListenForNotificationsDeliversPayloadsUntilCancelled(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	pool, err := Connect(ctx, testDatabaseURL(t), Options{MaxConnections: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	channel := fmt.Sprintf("postgres_test_%d", time.Now().UnixNano())
	payloads := make(chan string, 16)
	listenContext, stopListening := context.WithCancel(ctx)
	// Stop the listener before the pool closes, which waits for its connection.
	defer stopListening()
	stopped := make(chan error, 1)
	go func() {
		stopped <- ListenForNotifications(listenContext, pool, channel, func(payload string) { payloads <- payload })
	}()

	// LISTEN runs asynchronously, so keep notifying until one arrives.
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for received := false; !received; {
		if _, err := pool.Exec(ctx, "SELECT pg_notify($1, 'ready')", channel); err != nil {
			t.Fatal(err)
		}
		select {
		case payload := <-payloads:
			if payload != "ready" {
				t.Fatalf("payload = %q, want ready", payload)
			}
			received = true
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("no notification delivered")
		}
	}

	stopListening()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("listener stopped with %v after cancellation, want nil", err)
		}
	case <-ctx.Done():
		t.Fatal("listener did not stop after cancellation")
	}
}

func TestListenForNotificationsReportsAClosedPool(t *testing.T) {
	pool, err := Connect(t.Context(), testDatabaseURL(t), Options{MaxConnections: 1})
	if err != nil {
		t.Fatal(err)
	}
	pool.Close()
	if err := ListenForNotifications(t.Context(), pool, "postgres_test_closed", func(string) {}); err == nil {
		t.Fatal("listening on a closed pool succeeded")
	}
}
