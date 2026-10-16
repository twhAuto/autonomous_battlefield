//go:build !unix

// Drain signals exist only on Unix; the CLI worker runs on Linux.

package main

type drainer interface {
	Drain()
	Resume()
}

func handleDrainSignals(drainer) func() { return func() {} }
