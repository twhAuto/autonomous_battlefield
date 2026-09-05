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

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	aiworker "github.com/twh09/autonomous_battlefield/backend/internal/ai/worker"
	"github.com/twh09/autonomous_battlefield/backend/internal/config"
	"github.com/twh09/autonomous_battlefield/backend/internal/logging"
	"github.com/twh09/autonomous_battlefield/backend/internal/telemetry"
)

func main() {
	logger := logging.New("agent")
	slog.SetDefault(logger)
	configPath := flag.String("config", "config.yaml", "path to YAML configuration")
	flag.Parse()
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

func run(configuration config.Config) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	shutdownTracing, err := telemetry.ConfigureTracing(ctx, "battlefield-agent")
	if err != nil {
		return fmt.Errorf("configure tracing: %w", err)
	}
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownTracing(shutdown)
	}()
	worker := &aiworker.Worker{MCPURL: configuration.AI.MCPURL, Secret: configuration.AI.CapabilitySecret, WorkerID: hostname(), Profiles: ai.NewProfileRegistry(configuration.AI.Environment, configuration.AI.Profiles), PollInterval: time.Second, DecisionTimeout: configuration.AI.DecisionTimeout, Concurrency: 2}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		if !worker.Ready() {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	server := &http.Server{Addr: configuration.AI.AgentHealthAddress, Handler: telemetry.HTTPHandler("battlefield-agent.http", mux), ReadHeaderTimeout: 2 * time.Second}
	serverErrors := make(chan error, 1)
	go func() {
		err := server.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serverErrors <- err
	}()
	workerErrors := make(chan error, 1)
	go func() { workerErrors <- worker.Run(ctx) }()
	select {
	case err := <-serverErrors:
		return err
	case err := <-workerErrors:
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
