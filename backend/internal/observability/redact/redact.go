// Package redact removes database connection identities from error text before it
// reaches logs or traces.
package redact

import (
	"errors"
	"net"
	"reflect"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// maxErrors bounds the walk of an error tree, guarding against Unwrap cycles.
const maxErrors = 256

// Error renders err without the database user, database name, host or address that
// pgx connection errors and network errors print. Every matching error in the tree,
// including each branch of errors.Join, is rewritten; other text is kept, so
// application errors stay readable and a SQLSTATE survives as its code. A typed-nil
// error renders as "<nil>", and an error whose Error method panics never crashes the
// caller.
func Error(err error) (text string) {
	if err == nil {
		return ""
	}
	if isNil(err) {
		return "<nil>"
	}
	defer func() {
		if recover() != nil {
			text = "<unprintable error>"
		}
	}()
	text = err.Error()
	// Parents come before their causes, so a rewritten connection error also removes
	// the network error text nested inside it.
	for _, node := range tree(err) {
		switch e := node.(type) {
		case *pgconn.ConnectError:
			text = strings.ReplaceAll(text, e.Error(), connectFailure(e))
		case *net.OpError:
			text = strings.ReplaceAll(text, e.Error(), e.Op+" "+e.Net+": "+cause(e.Err))
		case *net.DNSError:
			text = strings.ReplaceAll(text, e.Error(), "lookup failed: "+e.Err)
		}
	}
	return text
}

// tree lists err and everything it wraps in pre-order, skipping typed-nil errors.
func tree(err error) []error {
	var nodes []error
	pending := []error{err}
	for len(pending) > 0 && len(nodes) < maxErrors {
		node := pending[0]
		pending = pending[1:]
		if node == nil || isNil(node) {
			continue
		}
		nodes = append(nodes, node)
		switch wrapper := node.(type) {
		case interface{ Unwrap() error }:
			pending = append([]error{wrapper.Unwrap()}, pending...)
		case interface{ Unwrap() []error }:
			pending = append(append([]error{}, wrapper.Unwrap()...), pending...)
		}
	}
	return nodes
}

func connectFailure(e *pgconn.ConnectError) string {
	text := "failed to connect to the database"
	var database *pgconn.PgError
	if errors.As(e.Unwrap(), &database) && database != nil {
		text += " (SQLSTATE " + database.Code + ")"
	}
	return text
}

// cause describes a network operation's failure without the name it was dialling.
func cause(err error) string {
	var lookup *net.DNSError
	if errors.As(err, &lookup) && lookup != nil {
		return "lookup failed: " + lookup.Err
	}
	return err.Error()
}

// isNil reports a non-nil interface holding a nil pointer.
func isNil(err error) bool {
	value := reflect.ValueOf(err)
	return value.Kind() == reflect.Pointer && value.IsNil()
}
