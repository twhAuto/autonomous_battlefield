//go:build unix

// Drains and resumes the worker on SIGUSR1 and SIGUSR2.

package main

import (
	"os"
	"os/signal"
	"syscall"
)

// drainer is the part of agent.Executor the drain signals control.
type drainer interface {
	Drain()
	Resume()
}

// handleDrainSignals stops new claims on SIGUSR1 and resumes them on SIGUSR2, so the
// CLI worker job can wait for running decisions before a restart. It returns a
// function that stops handling the signals.
func handleDrainSignals(worker drainer) func() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGUSR1, syscall.SIGUSR2)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case received := <-signals:
				if received == syscall.SIGUSR1 {
					worker.Drain()
				} else {
					worker.Resume()
				}
			case <-done:
				return
			}
		}
	}()
	return func() {
		signal.Stop(signals)
		close(done)
	}
}
