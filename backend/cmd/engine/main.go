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

	"github.com/twh09/autonomous_battlefield/backend/internal/battle/engine/worker"
	battlerepo "github.com/twh09/autonomous_battlefield/backend/internal/battle/repo"
	"github.com/twh09/autonomous_battlefield/backend/internal/config"
	"github.com/twh09/autonomous_battlefield/backend/internal/logging"
	"github.com/twh09/autonomous_battlefield/backend/internal/postgres"
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
	connectionContext, cancelConnection := context.WithTimeout(context.Background(), configuration.Database.ConnectTimeout)
	db, err := postgres.NewPostgresDB(connectionContext, configuration.Database.URL, postgres.Options{
		MaxConnections: configuration.Database.MaxConnections, MinConnections: configuration.Database.MinConnections,
		MaxConnectionLifetime: configuration.Database.MaxConnectionLifetime,
	})
	cancelConnection()
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer db.Close()
	slog.Info("database_connected")

	engineConfig := configuration.Engine
	engineWorkerID := workerID(engineConfig.WorkerIDEnvironmentVariable)
	engineWorker, err := worker.New(battlerepo.NewPostgres(db), worker.Config{
		WorkerID: engineWorkerID, PollInterval: engineConfig.PollInterval,
		LeaseDuration: engineConfig.LeaseDuration, HeartbeatInterval: engineConfig.HeartbeatInterval, ShutdownTimeout: engineConfig.ShutdownTimeout,
		MaxAttempts: engineConfig.MaxAttempts,
	})
	if err != nil {
		return err
	}

	healthServer := &http.Server{
		Addr: engineConfig.HealthAddress, Handler: worker.HealthHandler(engineWorker, db),
		ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 2 * time.Second,
		WriteTimeout: 2 * time.Second, IdleTimeout: 30 * time.Second,
		ErrorLog: slog.NewLogLogger(slog.Default().Handler(), slog.LevelError),
	}
	signalContext, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	runContext, cancelRun := context.WithCancel(signalContext)
	defer cancelRun()

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
	workerErrors := make(chan error, 1)
	go func() {
		workerErrors <- engineWorker.Run(runContext)
		cancelRun()
	}()

	<-runContext.Done()
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
