package migrations

import (
	"io/fs"
	"slices"
	"testing"
)

func TestFilesIncludesSQLMigrations(t *testing.T) {
	files, err := fs.Glob(Files, "*.sql")
	if err != nil {
		t.Fatalf("glob embedded migrations: %v", err)
	}

	if !slices.Contains(files, "00001_create_factions.sql") {
		t.Fatalf("embedded migrations = %v, want 00001_create_factions.sql", files)
	}
	if !slices.Contains(files, "00002_create_battle_queue.sql") {
		t.Fatalf("embedded migrations = %v, want 00002_create_battle_queue.sql", files)
	}
}
