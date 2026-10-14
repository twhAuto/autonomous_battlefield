// Package redact removes database connection identities from error text before it
// reaches logs or traces.
package redact

import (
	"errors"
	"net"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// Error renders err without the database user, database name, host or address that
// pgx connection errors and network errors print. Other error text is kept, so
// application errors stay readable, and a SQLSTATE survives as its code.
func Error(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	var connect *pgconn.ConnectError
	if errors.As(err, &connect) {
		replacement := "failed to connect to the database"
		var database *pgconn.PgError
		if errors.As(connect.Unwrap(), &database) {
			replacement += " (SQLSTATE " + database.Code + ")"
		}
		text = strings.Replace(text, connect.Error(), replacement, 1)
	}
	var operation *net.OpError
	if errors.As(err, &operation) {
		text = strings.Replace(text, operation.Error(), operation.Op+" "+operation.Net+": "+cause(operation.Err), 1)
	}
	var lookup *net.DNSError
	if errors.As(err, &lookup) {
		text = strings.Replace(text, lookup.Error(), "lookup failed: "+lookup.Err, 1)
	}
	return text
}

// cause describes a network operation's failure without the name it was dialling.
func cause(err error) string {
	var lookup *net.DNSError
	if errors.As(err, &lookup) {
		return "lookup failed: " + lookup.Err
	}
	return err.Error()
}
