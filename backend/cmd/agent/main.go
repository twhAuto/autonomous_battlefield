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
	"os/signal"
	"syscall"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai/agent"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/cliisolation"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/profile"
	"github.com/twh09/autonomous_battlefield/backend/internal/config"
	"github.com/twh09/autonomous_battlefield/backend/internal/observability/logging"
	"github.com/twh09/autonomous_battlefield/backend/internal/observability/tracing"
)

func main() {
	logger := logging.New("agent")
	slog.SetDefault(logger)
	configPath := flag.String("config", "config.yaml", "path to YAML configuration")
	checkAntigravityAuth := flag.Bool("check-antigravity-auth", false, "check isolated Antigravity keyring login without model inference")
	checkClaudeAuth := flag.Bool("check-claude-auth", false, "check Claude subscription login without model inference")
	flag.Parse()
	if *checkClaudeAuth {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := cliisolation.CheckClaudeAuth(ctx); err != nil {
			logger.Error("claude_auth_unavailable", "error", err)
			os.Exit(1)
		}
		return
	}
	if *checkAntigravityAuth {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := cliisolation.CheckAntigravityAuth(ctx); err != nil {
			logger.Error("antigravity_auth_unavailable", "error", err)
			os.Exit(1)
		}
		return
	}
	configuration, err := config.LoadAgent(*configPath)
	if err != nil {
		logger.Error("configuration_load_failed", "error", err)
		os.Exit(1)
	}
	if err := run(configuration); err != nil {
		logger.Error("agent_process_stopped", "error", err)
		os.Exit(1)
	}
}

func run(configuration config.AgentConfig) error {
	if err := agent.ProtectCredentials(configuration.Production); err != nil {
		return fmt.Errorf("protect agent credentials: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	shutdownTracing, err := tracing.Configure(ctx, "battlefield-agent")
	if err != nil {
		return fmt.Errorf("configure tracing: %w", err)
	}
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownTracing(shutdown)
	}()
	executor := &agent.Executor{PolicyPolling: true, MCPURL: configuration.Agent.MCPURL, Secret: configuration.Agent.Secret, WorkerID: hostname(), Profiles: profile.NewRegistry(configuration.Models.Environment, configuration.Models.Profiles), PollInterval: time.Second, DecisionTimeout: configuration.Models.DecisionTimeout, Concurrency: configuration.Agent.Concurrency}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		if !executor.Ready() {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	server := &http.Server{Addr: configuration.Agent.HealthAddress, Handler: tracing.HTTPHandler("battlefield-agent.http", mux), ReadHeaderTimeout: 2 * time.Second}
	serverErrors := make(chan error, 1)
	go func() {
		err := server.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serverErrors <- err
	}()
	executorErrors := make(chan error, 1)
	go func() { executorErrors <- executor.Run(ctx) }()
	select {
	case err := <-serverErrors:
		return err
	case err := <-executorErrors:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return server.Shutdown(shutdown)
}

func hostname() string {
	value, err := os.Hostname()
	if err != nil || value == "" {
		return fmt.Sprintf("agent-%d", os.Getpid())
	}
	return value
}
