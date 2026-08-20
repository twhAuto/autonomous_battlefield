package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/twh09/autonomous_battlefield/backend/internal/app"
	"github.com/twh09/autonomous_battlefield/backend/internal/config"
	"github.com/twh09/autonomous_battlefield/backend/internal/database"
)

func main() {
	options, err := parseOptions(os.Args[1:])
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		log.Fatal(err)
	}

	configuration, err := config.Load(options.configPath)
	if err != nil {
		log.Fatalf("load configuration: %v", err)
	}

	if options.migrate {
		migrationContext, stop := signal.NotifyContext(
			context.Background(),
			syscall.SIGINT,
			syscall.SIGTERM,
		)
		defer stop()

		applied, err := database.Migrate(
			migrationContext,
			configuration.Database.URL,
			configuration.Database.ConnectTimeout,
		)
		if err != nil {
			log.Fatalf("migrate database: %v", err)
		}
		log.Printf("Database migrations complete (%d applied)", applied)
		return
	}

	if err := startServer(configuration); err != nil {
		log.Fatal(err)
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
	db, err := database.NewPostgresDB(connectionContext, configuration.Database.URL, database.Options{
		MaxConnections:        configuration.Database.MaxConnections,
		MinConnections:        configuration.Database.MinConnections,
		MaxConnectionLifetime: configuration.Database.MaxConnectionLifetime,
	})
	cancelConnection()
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer db.Close()

	application := app.New(db)
	httpConfig := configuration.HTTP

	server := &http.Server{
		Addr:              httpConfig.Address,
		Handler:           application.HTTPHandler(),
		ReadHeaderTimeout: httpConfig.ReadHeaderTimeout,
		ReadTimeout:       httpConfig.ReadTimeout,
		WriteTimeout:      httpConfig.WriteTimeout,
		IdleTimeout:       httpConfig.IdleTimeout,
	}

	shutdownSignal, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()

	go func() {
		<-shutdownSignal.Done()

		shutdownContext, cancel := context.WithTimeout(context.Background(), httpConfig.ShutdownTimeout)
		defer cancel()

		if err := server.Shutdown(shutdownContext); err != nil {
			log.Printf("HTTP server shutdown failed: %v", err)
		}
	}()

	log.Printf("Autonomous Battlefield is listening on %s", server.Addr)

	if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	return nil
}
