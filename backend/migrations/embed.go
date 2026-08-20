// Package migrations exposes the database migrations embedded in the server
// binary.
package migrations

import "embed"

// Files contains every SQL migration in this directory.
//
//go:embed *.sql
var Files embed.FS
