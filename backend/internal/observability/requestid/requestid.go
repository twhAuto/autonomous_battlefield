// Package requestid carries the per-request correlation identifier through a context.
// It is a leaf package so that any handler package, including authn, can read it
// without an import cycle through httpapi.
package requestid

import "context"

type contextKey struct{}

// With returns a context carrying the request identifier.
func With(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, contextKey{}, id)
}

// From reports the request identifier in scope, or an empty string when none is set.
func From(ctx context.Context) string {
	value, _ := ctx.Value(contextKey{}).(string)
	return value
}
