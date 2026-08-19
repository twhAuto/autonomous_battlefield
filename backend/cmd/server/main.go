package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os/signal"
	"syscall"

	"github.com/twh09/autonomous_battlefield/backend/internal/app"
	"github.com/twh09/autonomous_battlefield/backend/internal/config"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to the YAML configuration file")
	flag.Parse()

	configuration, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load configuration: %v", err)
	}

	if err := startServer(configuration.HTTP); err != nil {
		log.Fatal(err)
	}
}

func startServer(httpConfig config.HTTPServer) error {
	application := app.New()

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
