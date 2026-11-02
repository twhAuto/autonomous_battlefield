// Starts the HTTP API or runs migrations, wiring configuration, database access, scenario
// catalogs, and process health.

package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twh09/autonomous_battlefield/backend/cmd/internal/startup"
	"github.com/twh09/autonomous_battlefield/backend/cmd/internal/startup/database"
	"github.com/twh09/autonomous_battlefield/backend/gamedata"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/profile"
	"github.com/twh09/autonomous_battlefield/backend/internal/app"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	"github.com/twh09/autonomous_battlefield/backend/internal/battle/catalog"
	"github.com/twh09/autonomous_battlefield/backend/internal/battle/resultcache"
	"github.com/twh09/autonomous_battlefield/backend/internal/config"
	"github.com/twh09/autonomous_battlefield/backend/internal/httpapi"
	"github.com/twh09/autonomous_battlefield/backend/internal/members"
	"github.com/twh09/autonomous_battlefield/backend/internal/observability/logging"
	"github.com/twh09/autonomous_battlefield/backend/internal/observability/metrics"
	"github.com/twh09/autonomous_battlefield/backend/internal/postgres"
)

func main() {
	slog.SetDefault(logging.New("api"))
	os.Exit(command().Main(os.Args[1:]))
}

// command runs the API, or with -migrate applies pending migrations and exits.
func command() startup.Command[config.APIConfig] {
	var migrate bool
	return startup.Command[config.APIConfig]{
		Name: "api",
		Flags: func(flags *flag.FlagSet) {
			flags.BoolVar(&migrate, "migrate", false, "apply pending database migrations and exit")
		},
		Load: config.LoadAPI,
		Run: func(ctx context.Context, configuration config.APIConfig) error {
			if migrate {
				return applyMigrations(ctx, configuration.Database)
			}
			return startServer(ctx, configuration)
		},
	}
}

func applyMigrations(ctx context.Context, database config.Database) error {
	slog.Info("database_migration_started")
	applied, err := postgres.Migrate(ctx, database.URL, database.ConnectTimeout)
	if err != nil {
		return fmt.Errorf("database migration: %w", err)
	}
	slog.Info("database_migration_completed", "applied", applied)
	return nil
}

// startServer serves the API until shutdownSignal is cancelled. Probes answer from the
// start; the application is activated once the database and catalog are ready.
func startServer(shutdownSignal context.Context, configuration config.APIConfig) error {
	processContext, cancelProcess := context.WithCancel(shutdownSignal)
	defer cancelProcess()
	stopTracing, err := startup.Tracing(processContext, "battlefield-api")
	if err != nil {
		return err
	}
	defer stopTracing()

	lifecycle := httpapi.NewLifecycleHandler(metrics.Handler())
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
	slog.Info("api_health_listening", "address", server.Addr)
	serverErrors := startup.Serve(server, cancelProcess)

	db, err := database.Connect(processContext, configuration.Database)
	if err != nil {
		return startup.AbortStart(server, lifecycle, serverErrors, shutdownSignal, httpConfig.ShutdownTimeout, err)
	}
	defer db.Close()
	slog.Info("database_connected")

	catalog, err := catalog.LoadCatalog(gamedata.Files)
	if err != nil {
		return startup.AbortStart(server, lifecycle, serverErrors, shutdownSignal, httpConfig.ShutdownTimeout, fmt.Errorf("load scenario catalog: %w", err))
	}
	slog.Info("scenario_catalog_loaded", "scenario_count", len(catalog.IDs()))
	profiles := profile.NewCatalog(configuration.Models.Environment, configuration.Models.Profiles)
	cache, err := resultcache.New(configuration.ResultCache.URL, configuration.ResultCache.TTL, configuration.ResultCache.Timeout)
	if err != nil {
		return startup.AbortStart(server, lifecycle, serverErrors, shutdownSignal, httpConfig.ShutdownTimeout, err)
	}
	defer cache.Close()
	memberStore, stopMembers, err := startManagedPlayers(processContext, db, configuration.Members, configuration.Auth.Roles)
	if err != nil {
		return startup.AbortStart(server, lifecycle, serverErrors, shutdownSignal, httpConfig.ShutdownTimeout, err)
	}
	defer stopMembers()
	if memberStore != nil {
		configuration.Auth.ResolveMember = memberStore.ActiveRole
	}
	application := app.New(db, catalog, configuration.API.MaxTicks, app.Options{Auth: configuration.Auth, Cache: cache, Generation: &configuration.Generation, Profiles: profiles, Members: membersRepository(memberStore), EngineConcurrency: configuration.API.EngineConcurrency})
	lifecycle.Activate(application.HTTPHandler(), db, nil)
	stopLeagues := application.StartLeagueScheduler(processContext)
	defer stopLeagues()
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

// startManagedPlayers imports configured Players and starts the Cloudflare group
// reconciler. It returns a nil store and a no-op stop when managed Players are off.
func startManagedPlayers(ctx context.Context, db *pgxpool.Pool, managed config.Members, roles map[string]authn.Role) (*members.Store, func(), error) {
	if !managed.Enabled {
		return nil, func() { /* Nothing was started. */ }, nil
	}
	store, err := members.NewStore(db, roles, members.NewCloudflare(members.Config{AccountID: managed.AccountID, GroupID: managed.GroupID, Token: managed.Token}))
	if err != nil {
		return nil, nil, err
	}
	stop, err := store.Start(ctx)
	return store, stop, err
}

// membersRepository keeps a disabled feature a nil interface, which the handler
// answers with 503, rather than an interface holding a nil *Store.
func membersRepository(store *members.Store) members.Repository {
	if store == nil {
		return nil
	}
	return store
}
