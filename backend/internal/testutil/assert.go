// Package testutil provides fatal assertions with lazy failure messages for tests.
package testutil

import "testing"

// Require stops the test when a condition is false. The failure callback is lazy
// so diagnostics can safely inspect values only on the failed branch.
func Require(t testing.TB, condition bool, failure func()) {
	t.Helper()
	if !condition {
		failure()
	}
}
