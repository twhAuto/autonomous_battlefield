package postgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPostgresRetryStopsWhenContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	attempts := 0

	_, err := NewPostgresDBWithRetry(
		ctx,
		"postgres://battlefield:battlefield@127.0.0.1:1/battlefield?sslmode=disable",
		Options{MaxConnections: 1},
		10*time.Millisecond,
		5*time.Millisecond,
		func(_ int, _ error) { attempts++ },
	)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context deadline exceeded", err)
	}
	if attempts == 0 {
		fatalf := "retry callback was not invoked"
		t.Fatal(fatalf)
	}
}

func TestPostgresRetryRejectsInvalidConfigurationWithoutRetrying(t *testing.T) {
	attempts := 0
	_, err := NewPostgresDBWithRetry(
		context.Background(),
		"://invalid",
		Options{},
		time.Second,
		time.Second,
		func(_ int, _ error) { attempts++ },
	)

	if err == nil {
		t.Fatal("expected invalid PostgreSQL URL to fail")
	}
	if attempts != 0 {
		t.Fatalf("retry attempts = %d, want 0", attempts)
	}
}
