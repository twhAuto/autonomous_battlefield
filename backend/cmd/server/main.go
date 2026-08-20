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

	"github.com/twh09/autonomous_battlefield/backend/internal/app"
	"github.com/twh09/autonomous_battlefield/backend/internal/battle/server/catalog"
	"github.com/twh09/autonomous_battlefield/backend/internal/config"
	"github.com/twh09/autonomous_battlefield/backend/internal/logging"
	"github.com/twh09/autonomous_battlefield/backend/internal/postgres"
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
	connectionContext, cancelConnection := context.WithTimeout(
		context.Background(),
		configuration.Database.ConnectTimeout,
	)
	db, err := postgres.NewPostgresDB(connectionContext, configuration.Database.URL, postgres.Options{
		MaxConnections:        configuration.Database.MaxConnections,
		MinConnections:        configuration.Database.MinConnections,
		MaxConnectionLifetime: configuration.Database.MaxConnectionLifetime,
	})
	cancelConnection()
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer db.Close()
	slog.Info("database_connected")

	catalog, err := catalog.LoadCatalog(configuration.API.ScenarioCatalogPath)
	if err != nil {
		return fmt.Errorf("load scenario catalog: %w", err)
	}
	slog.Info("scenario_catalog_loaded", "path", configuration.API.ScenarioCatalogPath, "scenario_count", len(catalog.IDs()))
	application := app.NewAPI(db, catalog, configuration.API.MaxTicks)
	httpConfig := configuration.HTTP

	server := &http.Server{
		Addr:              httpConfig.Address,
		Handler:           application.HTTPHandler(),
		ReadHeaderTimeout: httpConfig.ReadHeaderTimeout,
		ReadTimeout:       httpConfig.ReadTimeout,
		WriteTimeout:      httpConfig.WriteTimeout,
		IdleTimeout:       httpConfig.IdleTimeout,
		ErrorLog:          slog.NewLogLogger(slog.Default().Handler(), slog.LevelError),
	}

	shutdownSignal, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()

	go func() {
		<-shutdownSignal.Done()
		slog.Info("api_shutdown_started")

		shutdownContext, cancel := context.WithTimeout(context.Background(), httpConfig.ShutdownTimeout)
		defer cancel()

		if err := server.Shutdown(shutdownContext); err != nil {
			slog.Error("api_shutdown_failed", "error", err)
			return
		}
		slog.Info("api_shutdown_completed")
	}()

	slog.Info("api_listening", "address", server.Addr)

	if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	return nil
}
