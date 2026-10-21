// Package gamedata embeds the authored game content in the server binary: versioned
// content libraries, rulesets that select from them, and scenarios that use a
// ruleset. battle/catalog validates and resolves it at startup.
package gamedata

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
)

// Files holds the content, rulesets and scenarios directories.
//
//go:embed battlefield.yaml content rulesets scenarios
var Files embed.FS

// Digest returns a SHA-256 over every embedded file's path and contents, in path
// order, identifying the exact game content a binary carries.
func Digest() (string, error) {
	return digest(Files)
}

func digest(fsys fs.FS) (string, error) {
	hash := sha256.New()
	err := fs.WalkDir(fsys, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		// Length-prefix both fields so no two bundles hash the same byte stream.
		_, _ = fmt.Fprintf(hash, "%d:%s%d:", len(path), path, len(data))
		_, _ = hash.Write(data)
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
