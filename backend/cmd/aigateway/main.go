// Starts the AI gateway: the MCP tool server and the agent dispatch API, with restricted
// repositories and database wakeup notifications.

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
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/mcpserver"
	battlerepo "github.com/twh09/autonomous_battlefield/backend/internal/battle/repo"
	"github.com/twh09/autonomous_battlefield/backend/internal/config"
	"github.com/twh09/autonomous_battlefield/backend/internal/generation"
	"github.com/twh09/autonomous_battlefield/backend/internal/logging"
	"github.com/twh09/autonomous_battlefield/backend/internal/postgres"
	"github.com/twh09/autonomous_battlefield/backend/internal/telemetry"
)

func main() {
	logger := logging.New("ai-gateway")
	slog.SetDefault(logger)
	configPath := flag.String("config", "config.yaml", "path to YAML configuration")
	flag.Parse()
	configuration, err := config.LoadAIGateway(*configPath)
	if err != nil {
		logger.Error("configuration_load_failed", "error", err)
		os.Exit(1)
	}
	if err := run(configuration); err != nil {
		logger.Error("ai_gateway_process_stopped", "error", err)
		os.Exit(1)
	}
}

func run(configuration config.Config) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	shutdownTracing, err := telemetry.ConfigureTracing(ctx, "battlefield-ai-gateway")
	if err != nil {
		return fmt.Errorf("configure tracing: %w", err)
	}
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownTracing(shutdown)
	}()
	db, err := postgres.NewPostgresDBWithRetry(ctx, configuration.Database.URL, postgres.Options{MaxConnections: configuration.Database.MaxConnections, MinConnections: configuration.Database.MinConnections, MaxConnectionLifetime: configuration.Database.MaxConnectionLifetime}, configuration.Database.ConnectTimeout, time.Second, nil)
	if err != nil {
		return err
	}
	defer db.Close()
	repository := battlerepo.NewPostgres(db)
	server := mcpserver.New(repository, configuration.AI.CapabilitySecret, configuration.AI.AllowedOrigins).WithAgentSecret(configuration.AI.AgentSecret).WithGenerations(&generation.Postgres{DB: db})
	notificationErrors := make(chan error, 1)
	go func() {
		notificationErrors <- postgres.ListenForNotifications(ctx, db, ai.DecisionReadyChannel, func(string) {
			server.NotifyDecisionAvailable()
		})
	}()
	mux := http.NewServeMux()
	mux.Handle("/mcp", server.Handler())
	mux.Handle("/generation-mcp", server.GenerationHandler())
	mux.Handle("/internal/agent/", http.StripPrefix("/internal/agent", server.AgentHandler(repository)))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /readyz", readinessHandler(db, 2*time.Second))
	httpServer := &http.Server{Addr: configuration.AI.AIGatewayAddress, Handler: telemetry.HTTPHandler("battlefield-ai-gateway.http", mux), ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second}
	errorsChannel := make(chan error, 1)
	go func() {
		err := httpServer.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errorsChannel <- err
	}()
	slog.InfoContext(ctx, "ai_gateway_ready", "address", httpServer.Addr)
	select {
	case err := <-errorsChannel:
		return err
	case err := <-notificationErrors:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdown); err != nil {
		return fmt.Errorf("shutdown AI gateway: %w", err)
	}
	if err := <-errorsChannel; err != nil {
		return err
	}
	return <-notificationErrors
}

type pinger interface {
	Ping(context.Context) error
}

// readinessHandler reports ready only while PostgreSQL answers within timeout, so a
// lost database connection removes the gateway from Service endpoints. /healthz stays a
// process-liveness check that scripts use to confirm port-forwards.
func readinessHandler(database pinger, timeout time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		if err := database.Ping(ctx); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
