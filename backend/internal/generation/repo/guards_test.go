// Checks lookups reject malformed IDs and admission requires a known runtime, both
// before any database access.

package repo

import (
	"context"
	"errors"
	"testing"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/generation"
)

func TestMalformedIDsReadAsNotFound(t *testing.T) {
	p := NewPostgres(nil) // Any database access would panic.
	ctx := context.Background()
	for _, id := range []string{"", "job", "7f22e693-bacd-46bb-b942-64fa613cd38", "7f22e693-bacd-46bb-b942-64fa613cd388' OR '1'='1"} {
		if _, err := p.Get(ctx, "actor", id); !errors.Is(err, generation.ErrNotFound) {
			t.Fatalf("Get(%q) error = %v", id, err)
		}
		if _, err := p.Context(ctx, id); !errors.Is(err, generation.ErrNotFound) {
			t.Fatalf("Context(%q) error = %v", id, err)
		}
		if ok, err := p.Submit(ctx, id, "A history"); ok || err != nil {
			t.Fatalf("Submit(%q) = %t, %v", id, ok, err)
		}
		if ok, err := p.Finish(ctx, id, "", ai.Usage{}, nil); ok || err != nil {
			t.Fatalf("Finish(%q) = %t, %v", id, ok, err)
		}
	}
}

func TestAdmitRequiresProfileAndRuntime(t *testing.T) {
	p := NewPostgres(nil)
	for _, model := range []generation.Admission{{ProfileID: "writer"}, {ProfileID: "writer", Runtime: "local"}, {Runtime: "cli"}} {
		if _, _, err := p.Admit(context.Background(), "actor", "key", draft, generation.DefaultConfig(), model); !errors.Is(err, generation.ErrInvalid) {
			t.Fatalf("Admit(%+v) error = %v, want ErrInvalid", model, err)
		}
	}
}
