// Checks errors reach traces only through tracing.RecordError, which redacts database
// connection identities. Logs need no check: the logging handler redacts every error
// attribute itself.

package conventions

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const moduleRoot = "../.."

// directSpanError matches span.RecordError; the tracing.RecordError helper is allowed.
var directSpanError = regexp.MustCompile(`(\w+)\.RecordError\(`)

func TestSpanErrorsAreRedacted(t *testing.T) {
	scanned := 0
	for _, root := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(moduleRoot, root), func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			source, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			scanned++
			checkSpanErrors(t, filepath.ToSlash(path), string(source))
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if scanned == 0 {
		t.Fatal("no Go sources scanned")
	}
}

func checkSpanErrors(t *testing.T, path, source string) {
	t.Helper()
	if strings.HasSuffix(path, "internal/observability/tracing/tracing.go") {
		return
	}
	for number, line := range strings.Split(source, "\n") {
		if match := directSpanError.FindStringSubmatch(line); match != nil && match[1] != "tracing" {
			t.Errorf("%s:%d records a raw span error; use tracing.RecordError(span, err)", path, number+1)
		}
	}
}
