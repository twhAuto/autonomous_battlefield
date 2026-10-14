// Starts the durable battle engine with PostgreSQL, lifecycle probes, tracing, and signal-
// driven worker shutdown.

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
	battlerepo "github.com/twh09/autonomous_battlefield/backend/internal/battle/repo"
	"github.com/twh09/autonomous_battlefield/backend/internal/battle/worker"
	"github.com/twh09/autonomous_battlefield/backend/internal/config"
	"github.com/twh09/autonomous_battlefield/backend/internal/httpapi"
	"github.com/twh09/autonomous_battlefield/backend/internal/observability/logging"
	"github.com/twh09/autonomous_battlefield/backend/internal/observability/metrics"
)

func main() {
	slog.SetDefault(logging.New("engine"))
	os.Exit(startup.Command[config.EngineConfig]{Name: "engine", Load: config.LoadEngine, Run: startEngine}.Main(os.Args[1:]))
}

// startEngine runs the battle worker until signalContext is cancelled, then drains it.
func startEngine(signalContext context.Context, configuration config.EngineConfig) error {
	runContext, cancelRun := context.WithCancel(signalContext)
	defer cancelRun()
	stopTracing, err := startup.Tracing(runContext, "battlefield-engine")
	if err != nil {
		return err
	}
	defer stopTracing()

	engineConfig := configuration.Engine
	lifecycle := httpapi.NewLifecycleHandler(metrics.Handler())
	healthServer := &http.Server{
		Addr: engineConfig.HealthAddress, Handler: lifecycle,
		ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 2 * time.Second,
		WriteTimeout: 2 * time.Second, IdleTimeout: 30 * time.Second,
		ErrorLog: slog.NewLogLogger(slog.Default().Handler(), slog.LevelError),
	}
	slog.Info("engine_health_listening", "address", healthServer.Addr)
	healthErrors := startup.Serve(healthServer, cancelRun)

	db, err := database.Connect(runContext, configuration.Database)
	if err != nil {
		return startup.AbortStart(healthServer, lifecycle, healthErrors, signalContext, engineConfig.ShutdownTimeout, err)
	}
	defer db.Close()
	slog.Info("database_connected")

	engineWorkerID := engineConfig.WorkerID
	battles := battlerepo.NewPostgres(db)
	engineWorker, err := worker.New(battles, battles, worker.Config{KnowledgeRetrieval: configuration.Engine.KnowledgeRetrieval,
		WorkerID: engineWorkerID, PollInterval: engineConfig.PollInterval,
		LeaseDuration: engineConfig.LeaseDuration, HeartbeatInterval: engineConfig.HeartbeatInterval, ShutdownTimeout: engineConfig.ShutdownTimeout,
		MaxAttempts:     engineConfig.MaxAttempts,
		DecisionTimeout: configuration.DecisionTimeout,
	})
	if err != nil {
		return startup.AbortStart(healthServer, lifecycle, healthErrors, signalContext, engineConfig.ShutdownTimeout, err)
	}
	lifecycle.Activate(http.NotFoundHandler(), db, engineWorker.Ready)
	workerErrors := make(chan error, 1)
	go func() {
		workerErrors <- engineWorker.Run(runContext)
		cancelRun()
	}()

	<-runContext.Done()
	lifecycle.BeginShutdown()
	slog.Info("engine_shutdown_started", "worker_id", engineWorkerID)
	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), engineConfig.ShutdownTimeout)
	defer cancelShutdown()
	if err := healthServer.Shutdown(shutdownContext); err != nil {
		return fmt.Errorf("shutdown health server: %w", err)
	}
	select {
	case err := <-workerErrors:
		if err != nil {
			return err
		}
	case <-shutdownContext.Done():
		return fmt.Errorf("worker shutdown: %w", shutdownContext.Err())
	}
	select {
	case err := <-healthErrors:
		if err != nil {
			return err
		}
	default:
	}
	slog.Info("engine_shutdown_completed", "worker_id", engineWorkerID)
	return nil
}
