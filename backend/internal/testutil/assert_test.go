package testutil

import "testing"

func TestRequire(t *testing.T) {
	called := false
	Require(t, true, func() { called = true })
	if called {
		t.Fatal("successful assertion evaluated its diagnostic")
	}
	Require(t, false, func() { called = true })
	if !called {
		t.Fatal("failed assertion did not report its diagnostic")
	}
}
