// Checks Migrate fails fast when the database cannot be reached.

package postgres

import (
	"strings"
	"testing"
	"time"
)

func TestMigrateReportsAnUnreachableDatabase(t *testing.T) {
	_, err := Migrate(t.Context(), "postgres://battlefield@127.0.0.1:1/battlefield?sslmode=disable", time.Second)
	if err == nil || !strings.Contains(err.Error(), "ping PostgreSQL") {
		t.Fatalf("Migrate against a closed port = %v, want a ping error", err)
	}
}
