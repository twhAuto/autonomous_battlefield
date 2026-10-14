// Starts the model-agent process, wiring executable profiles, the job executor, health
// checks, and graceful shutdown.

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/cmd/internal/startup"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/agent"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/cliisolation"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/profile"
	"github.com/twh09/autonomous_battlefield/backend/internal/config"
	"github.com/twh09/autonomous_battlefield/backend/internal/observability/logging"
	"github.com/twh09/autonomous_battlefield/backend/internal/observability/tracing"
)

func main() {
	slog.SetDefault(logging.New("agent"))
	os.Exit(command(cliisolation.CheckClaudeAuth, cliisolation.CheckAntigravityAuth).Main(os.Args[1:]))
}

// authCheckTimeout bounds a subscription login check, which runs no model inference.
const authCheckTimeout = 15 * time.Second

// command runs the agent, or with a -check-*-auth flag only runs that subscription
// CLI's login check and exits.
func command(claudeAuth, antigravityAuth func(context.Context) error) startup.Command[config.AgentConfig] {
	var checkAntigravityAuth, checkClaudeAuth bool
	return startup.Command[config.AgentConfig]{
		Name: "agent",
		Flags: func(flags *flag.FlagSet) {
			flags.BoolVar(&checkAntigravityAuth, "check-antigravity-auth", false, "check isolated Antigravity keyring login without model inference")
			flags.BoolVar(&checkClaudeAuth, "check-claude-auth", false, "check Claude subscription login without model inference")
		},
		Before: func(ctx context.Context) (bool, error) {
			switch {
			case checkClaudeAuth:
				return true, checkAuth(ctx, "Claude", claudeAuth)
			case checkAntigravityAuth:
				return true, checkAuth(ctx, "Antigravity", antigravityAuth)
			}
			return false, nil
		},
		Load: config.LoadAgent,
		Run:  run,
	}
}

func checkAuth(ctx context.Context, cli string, check func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, authCheckTimeout)
	defer cancel()
	if err := check(ctx); err != nil {
		return fmt.Errorf("%s auth unavailable: %w", cli, err)
	}
	return nil
}

// shutdownTimeout bounds stopping the health server and waiting for in-flight jobs to
// report their outcomes, within Kubernetes' default 30-second grace period.
const shutdownTimeout = 20 * time.Second

// run executes agent jobs until ctx is cancelled, then waits for in-flight jobs.
func run(ctx context.Context, configuration config.AgentConfig) error {
	if err := agent.ProtectCredentials(configuration.Production); err != nil {
		return fmt.Errorf("protect agent credentials: %w", err)
	}
	stopTracing, err := startup.Tracing(ctx, "battlefield-agent")
	if err != nil {
		return err
	}
	defer stopTracing()
	executor := &agent.Executor{PolicyPolling: true, MCPURL: configuration.Agent.MCPURL, Secret: configuration.Agent.Secret, WorkerID: hostname(), Profiles: profile.NewRegistry(configuration.Models.Environment, configuration.Models.Profiles), PollInterval: time.Second, DecisionTimeout: configuration.Models.DecisionTimeout, Concurrency: configuration.Agent.Concurrency}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		if !executor.Ready() {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	server := &http.Server{
		Addr: configuration.Agent.HealthAddress, Handler: tracing.HTTPHandler("battlefield-agent.http", mux),
		ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 2 * time.Second,
		WriteTimeout: 2 * time.Second, IdleTimeout: 30 * time.Second,
	}
	return serveUntilStopped(ctx, server, executor, shutdownTimeout)
}

// jobRunner is the part of agent.Executor the process lifecycle needs.
type jobRunner interface {
	Run(context.Context) error
}

// serveUntilStopped runs the health server and the executor until either fails or ctx
// is cancelled. After cancellation it stops the health server and then waits for the
// executor: jobs report their outcome after cancellation, so exiting earlier would
// leave them running until their leases expire.
func serveUntilStopped(ctx context.Context, server *http.Server, executor jobRunner, timeout time.Duration) error {
	serverErrors := startup.Serve(server, nil)
	executorErrors := make(chan error, 1)
	go func() { executorErrors <- executor.Run(ctx) }()
	select {
	case err := <-serverErrors:
		return err
	case err := <-executorErrors:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := server.Shutdown(shutdown); err != nil {
		return fmt.Errorf("shut down agent health server: %w", err)
	}
	select {
	case err := <-executorErrors:
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	case <-shutdown.Done():
		return fmt.Errorf("agent jobs did not finish: %w", shutdown.Err())
	}
}

func hostname() string {
	value, err := os.Hostname()
	if err != nil || value == "" {
		return fmt.Sprintf("agent-%d", os.Getpid())
	}
	return value
}
