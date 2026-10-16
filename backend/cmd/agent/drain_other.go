//go:build !unix

// Drain signals exist only on Unix; the CLI worker runs on Linux.

package main

type drainer interface {
	Drain()
	Resume()
}

// handleDrainSignals registers nothing where SIGUSR1 and SIGUSR2 do not exist.
func handleDrainSignals(drainer) func() {
	return func() {
		// No signal handling was registered, so there is nothing to stop.
	}
}
