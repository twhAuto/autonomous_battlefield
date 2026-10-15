// Runs a backend binary's command line: options, configuration, signals and exit code.

package startup

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os/signal"
	"syscall"
)

// Command is a backend binary's process lifecycle around its own Run function.
type Command[T any] struct {
	// Name labels log events, as in "<Name>_process_stopped".
	Name string
	// Flags registers extra flags beyond -config; it may be nil.
	Flags func(*flag.FlagSet)
	// Before runs after the flags are parsed and before the configuration is loaded.
	// When it reports done, the process exits without loading configuration. It may
	// be nil.
	Before func(context.Context) (done bool, err error)
	// Load reads and validates the configuration file.
	Load func(path string) (T, error)
	// Run serves until its context is cancelled by SIGINT or SIGTERM.
	Run func(context.Context, T) error
}

// Main runs the command with arguments and returns the process exit code: 0 after a
// clean stop or -h, otherwise 1.
func (c Command[T]) Main(arguments []string) int {
	configPath, err := ParseFlags(c.Name, arguments, c.Flags)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		slog.Error("command_line_invalid", "error", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if c.Before != nil {
		done, err := c.Before(ctx)
		if err != nil {
			slog.Error(c.Name+"_process_stopped", "error", err)
			return 1
		}
		if done {
			return 0
		}
	}
	configuration, err := c.Load(configPath)
	if err != nil {
		slog.Error("configuration_load_failed", "config_path", configPath, "error", err)
		return 1
	}
	if err := c.Run(ctx, configuration); err != nil {
		slog.Error(c.Name+"_process_stopped", "error", err)
		return 1
	}
	return 0
}
