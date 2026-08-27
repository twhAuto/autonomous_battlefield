package migrations

import (
	"io/fs"
	"slices"
	"strings"
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
	if !slices.Contains(files, "00003_castle_siege_state.sql") {
		t.Fatalf("embedded migrations = %v, want 00003_castle_siege_state.sql", files)
	}
}

func TestCastleSiegeProceduralBlockIsKeptAsOneGooseStatement(t *testing.T) {
	contents, err := fs.ReadFile(Files, "00003_castle_siege_state.sql")
	if err != nil {
		t.Fatalf("read castle siege migration: %v", err)
	}

	sql := string(contents)
	begin := strings.Index(sql, "-- +goose StatementBegin")
	block := strings.Index(sql, "DO $$")
	end := strings.Index(sql, "-- +goose StatementEnd")
	if begin < 0 || block < 0 || end < 0 || !(begin < block && block < end) {
		t.Fatalf("PostgreSQL DO block must be wrapped in Goose StatementBegin/StatementEnd directives")
	}
}
