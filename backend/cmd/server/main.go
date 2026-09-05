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
	"github.com/twh09/autonomous_battlefield/backend/internal/app"
	"github.com/twh09/autonomous_battlefield/backend/internal/battle/catalog"
	"github.com/twh09/autonomous_battlefield/backend/internal/battle/resultcache"
	"github.com/twh09/autonomous_battlefield/backend/internal/config"
	"github.com/twh09/autonomous_battlefield/backend/internal/httpapi"
	"github.com/twh09/autonomous_battlefield/backend/internal/logging"
	"github.com/twh09/autonomous_battlefield/backend/internal/postgres"
	"github.com/twh09/autonomous_battlefield/backend/internal/telemetry"
)

func main() {
	logger := logging.New("api")
	slog.SetDefault(logger)
	options, err := parseOptions(os.Args[1:])
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		logger.Error("command_line_invalid", "error", err)
		os.Exit(1)
	}

	configuration, err := config.Load(options.configPath)
	if err != nil {
		logger.Error("configuration_load_failed", "config_path", options.configPath, "error", err)
		os.Exit(1)
	}

	if options.migrate {
		logger.Info("database_migration_started")
		migrationContext, stop := signal.NotifyContext(
			context.Background(),
			syscall.SIGINT,
			syscall.SIGTERM,
		)
		defer stop()

		applied, err := postgres.Migrate(
			migrationContext,
			configuration.Database.URL,
			configuration.Database.ConnectTimeout,
		)
		if err != nil {
			logger.Error("database_migration_failed", "error", err)
			os.Exit(1)
		}
		logger.Info("database_migration_completed", "applied", applied)
		return
	}

	if err := startServer(configuration); err != nil {
		logger.Error("api_process_stopped", "error", err)
		os.Exit(1)
	}
}

type cliOptions struct {
	configPath string
	migrate    bool
}

func parseOptions(arguments []string) (cliOptions, error) {
	flags := flag.NewFlagSet("server", flag.ContinueOnError)
	configPath := flags.String("config", "config.yaml", "path to the YAML configuration file")
	migrate := flags.Bool("migrate", false, "apply pending database migrations and exit")

	if err := flags.Parse(arguments); err != nil {
		return cliOptions{}, err
	}
	if flags.NArg() != 0 {
		return cliOptions{}, fmt.Errorf("unexpected positional arguments: %v", flags.Args())
	}

	return cliOptions{configPath: *configPath, migrate: *migrate}, nil
}

func startServer(configuration config.Config) error {
	shutdownSignal, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()
	processContext, cancelProcess := context.WithCancel(shutdownSignal)
	defer cancelProcess()
	shutdownTracing, err := telemetry.ConfigureTracing(processContext, "battlefield-api")
	if err != nil {
		return fmt.Errorf("configure tracing: %w", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownTracing(ctx)
	}()

	lifecycle := httpapi.NewLifecycleHandler(telemetry.MetricsHandler())
	httpConfig := configuration.HTTP
	server := &http.Server{
		Addr:              httpConfig.Address,
		Handler:           lifecycle,
		ReadHeaderTimeout: httpConfig.ReadHeaderTimeout,
		ReadTimeout:       httpConfig.ReadTimeout,
		WriteTimeout:      httpConfig.WriteTimeout,
		IdleTimeout:       httpConfig.IdleTimeout,
		ErrorLog:          slog.NewLogLogger(slog.Default().Handler(), slog.LevelError),
	}
	serverErrors := make(chan error, 1)
	go func() {
		slog.Info("api_health_listening", "address", server.Addr)
		err := server.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serverErrors <- err
		cancelProcess()
	}()

	db, err := postgres.NewPostgresDBWithRetry(processContext, configuration.Database.URL, postgres.Options{
		MaxConnections:        configuration.Database.MaxConnections,
		MinConnections:        configuration.Database.MinConnections,
		MaxConnectionLifetime: configuration.Database.MaxConnectionLifetime,
	}, configuration.Database.ConnectTimeout, time.Second, func(attempt int, err error) {
		slog.Warn("database_connect_retry", "attempt", attempt, "retry_in", time.Second, "error", err)
	})
	if err != nil {
		return stopAPIInitialization(server, lifecycle, serverErrors, shutdownSignal, httpConfig.ShutdownTimeout, err)
	}
	defer db.Close()
	slog.Info("database_connected")

	catalog, err := catalog.LoadCatalog(configuration.API.ScenarioCatalogPath, configuration.API.RulesetCatalogPath)
	if err != nil {
		return stopAPIInitialization(server, lifecycle, serverErrors, shutdownSignal, httpConfig.ShutdownTimeout, fmt.Errorf("load scenario catalog: %w", err))
	}
	slog.Info("scenario_catalog_loaded", "path", configuration.API.ScenarioCatalogPath, "scenario_count", len(catalog.IDs()))
	profiles := ai.NewProfileCatalog(configuration.AI.Environment, configuration.AI.Profiles)
	cache, err := resultcache.New(configuration.ResultCache.URL, configuration.ResultCache.TTL, configuration.ResultCache.Timeout)
	if err != nil {
		return stopAPIInitialization(server, lifecycle, serverErrors, shutdownSignal, httpConfig.ShutdownTimeout, err)
	}
	defer cache.Close()
	application := app.NewAPIWithResultCache(db, catalog, configuration.API.MaxTicks, configuration.Auth, cache, profiles)
	lifecycle.Activate(application.HTTPHandler(), db, nil)
	slog.Info("api_ready", "address", server.Addr)

	select {
	case err := <-serverErrors:
		if err != nil {
			return fmt.Errorf("serve API: %w", err)
		}
		return nil
	case <-shutdownSignal.Done():
	}

	lifecycle.BeginShutdown()
	slog.Info("api_shutdown_started")
	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), httpConfig.ShutdownTimeout)
	defer cancelShutdown()
	if err := server.Shutdown(shutdownContext); err != nil {
		return fmt.Errorf("shutdown API: %w", err)
	}
	if err := <-serverErrors; err != nil {
		return fmt.Errorf("serve API: %w", err)
	}
	slog.Info("api_shutdown_completed")
	return nil
}

func stopAPIInitialization(
	server *http.Server,
	lifecycle *httpapi.LifecycleHandler,
	serverErrors <-chan error,
	shutdownSignal context.Context,
	shutdownTimeout time.Duration,
	initializationError error,
) error {
	lifecycle.BeginShutdown()
	shutdownContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownContext); err != nil {
		return fmt.Errorf("shutdown API after initialization failure: %w", err)
	}
	if err := <-serverErrors; err != nil {
		return fmt.Errorf("serve API: %w", err)
	}
	if shutdownSignal.Err() != nil {
		return nil
	}
	return fmt.Errorf("initialize API: %w", initializationError)
}
