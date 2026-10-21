// Checks the embedded game content is complete and its digest identifies every file
// path and byte.

package gamedata

import (
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestFilesEmbedEveryContentDirectory(t *testing.T) {
	for _, file := range []string{"battlefield.yaml", "content/core-v3.yaml", "rulesets/battlefield.yaml", "scenarios/castle-siege.yaml", "scenarios/frontier-siege.yaml"} {
		if _, err := fs.Stat(Files, file); err != nil {
			t.Fatalf("%s not embedded: %v", file, err)
		}
	}
}

func TestDigestChangesWithAnyPathOrByte(t *testing.T) {
	base := fstest.MapFS{"content/a.yaml": {Data: []byte("x: 1")}, "rulesets/b.yaml": {Data: []byte("y: 2")}}
	want, err := digest(base)
	if err != nil || len(want) != 64 {
		t.Fatalf("digest = %q, %v", want, err)
	}
	if again, _ := Digest(); again == want {
		t.Fatal("embedded digest equals the fixture digest")
	}
	for name, changed := range map[string]fstest.MapFS{
		"content":  {"content/a.yaml": {Data: []byte("x: 2")}, "rulesets/b.yaml": {Data: []byte("y: 2")}},
		"path":     {"content/c.yaml": {Data: []byte("x: 1")}, "rulesets/b.yaml": {Data: []byte("y: 2")}},
		"added":    {"content/a.yaml": {Data: []byte("x: 1")}, "rulesets/b.yaml": {Data: []byte("y: 2")}, "scenarios/c.yaml": {}},
		"boundary": {"content/a.yaml": {Data: []byte("x: 1rulesets/b.yamly: 2")}},
	} {
		if got, err := digest(changed); err != nil || got == want {
			t.Fatalf("%s change kept digest %q (%v)", name, got, err)
		}
	}
	if again, _ := digest(base); again != want {
		t.Fatal("digest is not deterministic")
	}
}

func TestDigestReportsUnreadableFiles(t *testing.T) {
	if _, err := digest(brokenFS{}); err == nil {
		t.Fatal("unreadable file system digested")
	}
}

type brokenFS struct{}

func (brokenFS) Open(string) (fs.File, error) { return nil, fs.ErrPermission }
