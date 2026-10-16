//go:build unix

// Checks that SIGUSR1 drains the worker and SIGUSR2 resumes it.

package main

import (
	"os"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

type drainRecorder struct{ draining atomic.Int32 }

func (r *drainRecorder) Drain()  { r.draining.Store(1) }
func (r *drainRecorder) Resume() { r.draining.Store(2) }

func TestDrainSignalsDrainAndResumeTheWorker(t *testing.T) {
	recorder := &drainRecorder{}
	stop := handleDrainSignals(recorder)
	defer stop()
	for signal, want := range map[syscall.Signal]int32{syscall.SIGUSR1: 1, syscall.SIGUSR2: 2} {
		if err := syscall.Kill(os.Getpid(), signal); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(2 * time.Second)
		for recorder.draining.Load() != want {
			if time.Now().After(deadline) {
				t.Fatalf("%v did not reach the worker", signal)
			}
			time.Sleep(time.Millisecond)
		}
	}
}
