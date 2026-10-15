// Package startup holds the process wiring the backend binaries share: command-line
// options, tracing, and stopping a half-started HTTP listener. The database pool is in
// startup/database, so binaries without database access (the agent) never import it.
package startup

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/observability/tracing"
)

// tracingShutdownTimeout bounds the final span flush when a process stops.
const tracingShutdownTimeout = 5 * time.Second

// ParseFlags parses a binary's arguments: -config, plus any flags register adds. It
// returns the configuration path, flag.ErrHelp for -h, and an error for positional
// arguments, which no binary accepts.
func ParseFlags(binary string, arguments []string, register func(*flag.FlagSet)) (string, error) {
	flags := flag.NewFlagSet(binary, flag.ContinueOnError)
	configPath := flags.String("config", "config.yaml", "path to the YAML configuration file")
	if register != nil {
		register(flags)
	}
	if err := flags.Parse(arguments); err != nil {
		return "", err
	}
	if flags.NArg() != 0 {
		return "", fmt.Errorf("unexpected positional arguments: %v", flags.Args())
	}
	return *configPath, nil
}

// Tracing configures trace export for service and returns a stop function that flushes
// pending spans within tracingShutdownTimeout.
func Tracing(ctx context.Context, service string) (func(), error) {
	shutdown, err := tracing.Configure(ctx, service)
	if err != nil {
		return nil, fmt.Errorf("configure tracing: %w", err)
	}
	return func() {
		flushContext, cancel := context.WithTimeout(context.Background(), tracingShutdownTimeout)
		defer cancel()
		_ = shutdown(flushContext)
	}, nil
}

// probes is the part of httpapi.LifecycleHandler AbortStart needs; naming the type
// would link httpapi into binaries that never serve it.
type probes interface {
	BeginShutdown()
}

// AbortStart stops a process whose probe listener is already serving but whose
// initialization failed. It fails the probes, shuts the listener down, and reports
// the listener's own error first. A shutdown signal is not a failure, so it returns
// nil then; otherwise it returns the initialization error.
func AbortStart(server *http.Server, lifecycle probes, serveErrors <-chan error, shutdownSignal context.Context, timeout time.Duration, initialization error) error {
	lifecycle.BeginShutdown()
	shutdownContext, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := server.Shutdown(shutdownContext); err != nil {
		return fmt.Errorf("shut down after failed start: %w", err)
	}
	if err := <-serveErrors; err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	if shutdownSignal.Err() != nil {
		return nil
	}
	return fmt.Errorf("initialize: %w", initialization)
}

// Serve runs server in the background and returns a channel that receives its result
// once: nil after a graceful shutdown, otherwise the listen or serve error. stopped,
// when set, runs after the result is sent, so a process can stop with its listener.
func Serve(server *http.Server, stopped func()) <-chan error {
	result := make(chan error, 1)
	go func() {
		err := server.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		result <- err
		if stopped != nil {
			stopped()
		}
	}()
	return result
}
