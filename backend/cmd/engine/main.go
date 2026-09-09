// Starts the durable battle engine with PostgreSQL, lifecycle probes, tracing, and signal-
// driven worker shutdown.

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
	"strconv"
	"syscall"
	"time"

	battlerepo "github.com/twh09/autonomous_battlefield/backend/internal/battle/repo"
	"github.com/twh09/autonomous_battlefield/backend/internal/battle/worker"
	"github.com/twh09/autonomous_battlefield/backend/internal/config"
	"github.com/twh09/autonomous_battlefield/backend/internal/httpapi"
	"github.com/twh09/autonomous_battlefield/backend/internal/logging"
	"github.com/twh09/autonomous_battlefield/backend/internal/postgres"
	"github.com/twh09/autonomous_battlefield/backend/internal/telemetry"
)

func main() {
	logger := logging.New("engine")
	slog.SetDefault(logger)
	options, err := parseOptions(os.Args[1:])
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		logger.Error("command_line_invalid", "error", err)
		os.Exit(1)
	}
	configuration, err := config.LoadEngine(options.configPath)
	if err != nil {
		logger.Error("configuration_load_failed", "config_path", options.configPath, "error", err)
		os.Exit(1)
	}
	if err := startEngine(configuration); err != nil {
		logger.Error("engine_process_stopped", "error", err)
		os.Exit(1)
	}
}

type cliOptions struct {
	configPath string
}

func parseOptions(arguments []string) (cliOptions, error) {
	flags := flag.NewFlagSet("engine", flag.ContinueOnError)
	configPath := flags.String("config", "config.yaml", "path to the YAML configuration file")
	if err := flags.Parse(arguments); err != nil {
		return cliOptions{}, err
	}
	if flags.NArg() != 0 {
		return cliOptions{}, fmt.Errorf("unexpected positional arguments: %v", flags.Args())
	}
	return cliOptions{configPath: *configPath}, nil
}

func startEngine(configuration config.Config) error {
	signalContext, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	runContext, cancelRun := context.WithCancel(signalContext)
	defer cancelRun()
	shutdownTracing, err := telemetry.ConfigureTracing(runContext, "battlefield-engine")
	if err != nil {
		return fmt.Errorf("configure tracing: %w", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownTracing(ctx)
	}()

	engineConfig := configuration.Engine
	lifecycle := httpapi.NewLifecycleHandler(telemetry.MetricsHandler())
	healthServer := &http.Server{
		Addr: engineConfig.HealthAddress, Handler: lifecycle,
		ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 2 * time.Second,
		WriteTimeout: 2 * time.Second, IdleTimeout: 30 * time.Second,
		ErrorLog: slog.NewLogLogger(slog.Default().Handler(), slog.LevelError),
	}
	healthErrors := make(chan error, 1)
	go func() {
		slog.Info("engine_health_listening", "address", healthServer.Addr)
		err := healthServer.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		healthErrors <- err
		cancelRun()
	}()

	db, err := postgres.NewPostgresDBWithRetry(runContext, configuration.Database.URL, postgres.Options{
		MaxConnections: configuration.Database.MaxConnections, MinConnections: configuration.Database.MinConnections,
		MaxConnectionLifetime: configuration.Database.MaxConnectionLifetime,
	}, configuration.Database.ConnectTimeout, time.Second, func(attempt int, err error) {
		slog.Warn("database_connect_retry", "attempt", attempt, "retry_in", time.Second, "error", err)
	})
	if err != nil {
		return stopEngineInitialization(healthServer, lifecycle, healthErrors, signalContext, engineConfig.ShutdownTimeout, err)
	}
	defer db.Close()
	slog.Info("database_connected")

	engineWorkerID := workerID(engineConfig.WorkerIDEnvironmentVariable)
	engineWorker, err := worker.New(battlerepo.NewPostgres(db), worker.Config{KnowledgeRetrieval: configuration.Engine.KnowledgeRetrieval,
		WorkerID: engineWorkerID, PollInterval: engineConfig.PollInterval,
		LeaseDuration: engineConfig.LeaseDuration, HeartbeatInterval: engineConfig.HeartbeatInterval, ShutdownTimeout: engineConfig.ShutdownTimeout,
		MaxAttempts:     engineConfig.MaxAttempts,
		DecisionTimeout: configuration.AI.DecisionTimeout, CapabilitySecret: configuration.AI.CapabilitySecret,
	})
	if err != nil {
		return stopEngineInitialization(healthServer, lifecycle, healthErrors, signalContext, engineConfig.ShutdownTimeout, err)
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

func stopEngineInitialization(
	healthServer *http.Server,
	lifecycle *httpapi.LifecycleHandler,
	healthErrors <-chan error,
	signalContext context.Context,
	shutdownTimeout time.Duration,
	initializationError error,
) error {
	lifecycle.BeginShutdown()
	shutdownContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := healthServer.Shutdown(shutdownContext); err != nil {
		return fmt.Errorf("shutdown engine health server after initialization failure: %w", err)
	}
	if err := <-healthErrors; err != nil {
		return fmt.Errorf("serve engine health: %w", err)
	}
	if signalContext.Err() != nil {
		return nil
	}
	return fmt.Errorf("initialize engine: %w", initializationError)
}

func workerID(environmentVariable string) string {
	if id := os.Getenv(environmentVariable); id != "" {
		return id
	}
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "engine"
	}
	return hostname + "-" + strconv.Itoa(os.Getpid())
}
