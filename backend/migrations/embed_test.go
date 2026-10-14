// Checks every embedded migration follows the Goose file conventions: contiguous
// version numbers, an Up and a Down section, dollar-quoted bodies inside balanced
// statement blocks, and a header comment.

package migrations

import (
	"fmt"
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

var migrationName = regexp.MustCompile(`^(\d{5})_[a-z0-9_]+\.sql$`)

func TestMigrationsAreNumberedWithoutGaps(t *testing.T) {
	files, err := fs.Glob(Files, "*.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("embedded migrations = %v, %v", files, err)
	}
	// fs.Glob returns names sorted, so the nth file must carry version n.
	for i, file := range files {
		match := migrationName.FindStringSubmatch(file)
		if match == nil {
			t.Fatalf("%s is not named NNNNN_lowercase_words.sql", file)
		}
		if want := fmt.Sprintf("%05d", i+1); match[1] != want {
			t.Fatalf("%s has version %s, want %s: versions must start at 00001 without gaps or duplicates", file, match[1], want)
		}
	}
}

func TestMigrationsHaveUpDownBalancedBlocksAndHeader(t *testing.T) {
	files, _ := fs.Glob(Files, "*.sql")
	for _, file := range files {
		contents, err := fs.ReadFile(Files, file)
		if err != nil {
			t.Fatal(err)
		}
		if problem := conventionProblem(string(contents)); problem != "" {
			t.Errorf("%s: %s", file, problem)
		}
	}
}

// conventionProblem describes the first Goose convention sql breaks, or returns "".
func conventionProblem(sql string) string {
	if !strings.HasPrefix(sql, "-- ") || strings.HasPrefix(sql, "-- +goose") {
		return "must start with a comment describing the change"
	}
	up, down := strings.Index(sql, "-- +goose Up"), strings.Index(sql, "-- +goose Down")
	if up < 0 || down < up || strings.Count(sql, "-- +goose Up") != 1 || strings.Count(sql, "-- +goose Down") != 1 {
		return "needs exactly one +goose Up section followed by one +goose Down section"
	}
	return statementBlockProblem(sql)
}

// statementBlockProblem checks statement blocks. PostgreSQL DO blocks and function
// bodies contain semicolons, so each dollar-quoted body must sit in one
// StatementBegin/StatementEnd pair for Goose to send it as one statement.
func statementBlockProblem(sql string) string {
	open := false
	for _, line := range strings.Split(sql, "\n") {
		if strings.Contains(line, "$$") && !open {
			return "dollar-quoted body outside a StatementBegin/StatementEnd block"
		}
		switch strings.TrimSpace(line) {
		case "-- +goose StatementBegin":
			if open {
				return "StatementBegin opened twice without StatementEnd"
			}
			open = true
		case "-- +goose StatementEnd":
			if !open {
				return "StatementEnd without StatementBegin"
			}
			open = false
		}
	}
	if open {
		return "StatementBegin never closed"
	}
	return ""
}

func TestConventionProblemDetectsEachViolation(t *testing.T) {
	castleSiege, err := fs.ReadFile(Files, "00003_castle_siege_state.sql")
	if err != nil {
		t.Fatal(err)
	}
	unwrapped := strings.NewReplacer("-- +goose StatementBegin\n", "", "-- +goose StatementEnd\n", "").Replace(string(castleSiege))
	if conventionProblem(unwrapped) == "" {
		t.Fatal("00003 accepted without its StatementBegin/StatementEnd directives")
	}
	valid := "-- Adds a table.\n-- +goose Up\n-- +goose StatementBegin\nDO $$ BEGIN END $$;\n-- +goose StatementEnd\n-- +goose Down\nSELECT 1;\n"
	if problem := conventionProblem(valid); problem != "" {
		t.Fatalf("valid migration rejected: %s", problem)
	}
	for name, sql := range map[string]string{
		"no header":      "-- +goose Up\nSELECT 1;\n-- +goose Down\nSELECT 1;\n",
		"no down":        "-- Adds.\n-- +goose Up\nSELECT 1;\n",
		"down before up": "-- Adds.\n-- +goose Down\nSELECT 1;\n-- +goose Up\nSELECT 1;\n",
		"two ups":        "-- Adds.\n-- +goose Up\n-- +goose Up\n-- +goose Down\n",
		"unclosed block": "-- Adds.\n-- +goose Up\n-- +goose StatementBegin\nDO $$ $$;\n-- +goose Down\n",
		"nested block":   "-- Adds.\n-- +goose Up\n-- +goose StatementBegin\n-- +goose StatementBegin\n-- +goose StatementEnd\n-- +goose Down\n",
		"stray end":      "-- Adds.\n-- +goose Up\n-- +goose StatementEnd\n-- +goose Down\n",
		"unwrapped DO":   "-- Adds.\n-- +goose Up\nDO $$ BEGIN PERFORM 1; END $$;\n-- +goose Down\nSELECT 1;\n",
		"unwrapped body": "-- Adds.\n-- +goose Up\nCREATE FUNCTION f() RETURNS int LANGUAGE sql AS $$\nSELECT 1;\n$$;\n-- +goose Down\n",
	} {
		if conventionProblem(sql) == "" {
			t.Errorf("%s accepted", name)
		}
	}
}
