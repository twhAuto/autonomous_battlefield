// Starts the AI gateway: the MCP tool server and the agent dispatch API, with restricted
// repositories and database wakeup notifications.

package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/cmd/internal/startup"
	"github.com/twh09/autonomous_battlefield/backend/cmd/internal/startup/database"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/mcpserver"
	battlerepo "github.com/twh09/autonomous_battlefield/backend/internal/battle/repo"
	"github.com/twh09/autonomous_battlefield/backend/internal/config"
	generationrepo "github.com/twh09/autonomous_battlefield/backend/internal/generation/repo"
	"github.com/twh09/autonomous_battlefield/backend/internal/observability/logging"
	"github.com/twh09/autonomous_battlefield/backend/internal/observability/tracing"
	"github.com/twh09/autonomous_battlefield/backend/internal/postgres"
)

func main() {
	slog.SetDefault(logging.New("ai-gateway"))
	os.Exit(startup.Command[config.AIGatewayConfig]{Name: "ai_gateway", Load: config.LoadAIGateway, Run: run}.Main(os.Args[1:]))
}

// run serves the MCP tools and agent dispatch API until ctx is cancelled.
func run(ctx context.Context, configuration config.AIGatewayConfig) error {
	stopTracing, err := startup.Tracing(ctx, "battlefield-ai-gateway")
	if err != nil {
		return err
	}
	defer stopTracing()
	db, err := database.Connect(ctx, configuration.Database)
	if err != nil {
		return err
	}
	defer db.Close()
	repository := battlerepo.NewPostgres(db)
	server := mcpserver.New(repository, configuration.Gateway.CapabilitySecret, configuration.Gateway.AllowedOrigins).WithAgentSecret(configuration.Gateway.AgentSecret).WithGenerations(generationrepo.NewPostgres(db))
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
	httpServer := &http.Server{Addr: configuration.Gateway.Address, Handler: tracing.HTTPHandler("battlefield-ai-gateway.http", mux), ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second}
	errorsChannel := startup.Serve(httpServer, nil)
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
