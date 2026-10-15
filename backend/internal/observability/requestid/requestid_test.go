package requestid

import (
	"context"
	"testing"
)

func TestFromReturnsTheIdentifierAttachedByWith(t *testing.T) {
	ctx := With(context.Background(), "req-1")
	if got := From(ctx); got != "req-1" {
		t.Fatalf("From = %q, want req-1", got)
	}
}

func TestFromIsEmptyWhenNoIdentifierIsAttached(t *testing.T) {
	if got := From(context.Background()); got != "" {
		t.Fatalf("From = %q, want empty", got)
	}
}
