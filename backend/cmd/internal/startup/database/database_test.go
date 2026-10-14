// Checks the pool connection keeps retrying until its context ends.

package database

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/config"
)

func TestConnectStopsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := Connect(ctx, config.Database{URL: "postgres://battlefield@127.0.0.1:1/battlefield?sslmode=disable", MaxConnections: 1, ConnectTimeout: 10 * time.Millisecond})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want the context deadline", err)
	}
}
