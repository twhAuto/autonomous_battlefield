// Composes API repositories, services, routes, and authentication middleware; process
// startup remains in cmd/server.

package app

import (
	"context"
	"net/http"

	"github.com/twh09/autonomous_battlefield/backend/internal/admin"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/profile"
	analyticsapi "github.com/twh09/autonomous_battlefield/backend/internal/analytics/api"
	analyticsrepo "github.com/twh09/autonomous_battlefield/backend/internal/analytics/repo"
	analyticsservice "github.com/twh09/autonomous_battlefield/backend/internal/analytics/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	battleapi "github.com/twh09/autonomous_battlefield/backend/internal/battle/api"
	"github.com/twh09/autonomous_battlefield/backend/internal/battle/catalog"
	battlerepo "github.com/twh09/autonomous_battlefield/backend/internal/battle/repo"
	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
	factionapi "github.com/twh09/autonomous_battlefield/backend/internal/faction/api"
	factionrepo "github.com/twh09/autonomous_battlefield/backend/internal/faction/repo"
	factionservice "github.com/twh09/autonomous_battlefield/backend/internal/faction/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/generation"
	generationapi "github.com/twh09/autonomous_battlefield/backend/internal/generation/api"
	generationrepo "github.com/twh09/autonomous_battlefield/backend/internal/generation/repo"
	"github.com/twh09/autonomous_battlefield/backend/internal/httpapi"
	leagueapi "github.com/twh09/autonomous_battlefield/backend/internal/league/api"
	leaguerepo "github.com/twh09/autonomous_battlefield/backend/internal/league/repo"
	leagueservice "github.com/twh09/autonomous_battlefield/backend/internal/league/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/members"
	"github.com/twh09/autonomous_battlefield/backend/internal/observability/tracing"
	"github.com/twh09/autonomous_battlefield/backend/internal/postgres"
)

// App is the application's composition root. It constructs and connects the
// repositories, services, and handlers used by the process.
type App struct {
	httpHandler http.Handler
	leagues     *leagueservice.Service
}

// Options configures optional API dependencies. Zero values select defaults: the local
// model catalog, no result cache, the default background-generation configuration, and
// no member store.
type Options struct {
	Auth       authn.Config
	Cache      battleservice.ResultCache
	Generation *generation.Config
	Profiles   *profile.Registry
	Members    members.Repository
}

// New composes the API from its database, scenario catalog, and battle tick limit.
func New(db postgres.Pool, catalog *catalog.Catalog, maxTicks uint64, options Options) *App {
	authentication := options.Auth
	profiles := options.Profiles
	if profiles == nil {
		profiles = profile.NewCatalog("local", nil)
	}
	generationConfig := generation.DefaultConfig()
	if options.Generation != nil {
		generationConfig = *options.Generation
	}
	adminService := &admin.Service{Repository: &admin.Postgres{DB: db}, Profiles: profiles}
	adminHandler := &admin.Handler{Service: adminService}
	profiles = profiles.WithAccessPolicy(adminService.ReadPolicy)
	generationHandler := generationapi.NewHandler(generationrepo.NewPostgres(db), generationConfig, profiles)
	factionRepository := factionrepo.NewPostgres(db)
	factionService := factionservice.NewService(factionRepository, catalog)
	battleService := battleservice.NewService(battlerepo.NewPostgres(db), factionRepository, catalog, maxTicks, battleservice.Options{Cache: options.Cache, Profiles: profiles})

	leagueService := leagueservice.New(leaguerepo.NewPostgres(db), battleService, factionRepository)
	analyticsService := analyticsservice.New(analyticsrepo.NewPostgres(db), currentRulesVersion(catalog))

	factionHandler := factionapi.NewHandler(factionService)
	battleHandler := battleapi.NewHandler(battleService)
	authenticationManager := authn.NewManager(authentication)
	authenticationMiddleware := func(next http.Handler) http.Handler {
		return authn.BrowserProtection(authentication.Origin, authn.RequireAuthentication(authenticationManager.Enabled(), authenticationManager, authn.RolePreview(authn.RoutePolicy(next))))
	}
	router := httpapi.NewRouterWithMiddleware(
		[]httpapi.Middleware{authenticationMiddleware},
		authn.NewRoutes(authenticationManager), factionHandler, battleHandler, leagueapi.NewHandler(leagueService), analyticsapi.NewHandler(analyticsService), generationHandler, adminHandler, members.NewHandler(options.Members, authentication.Origin),
	)
	// Tracing wraps access logging so completion logs share the request span.
	return &App{httpHandler: tracing.HTTPHandler("battlefield-api.http", router), leagues: leagueService}
}

func (a *App) HTTPHandler() http.Handler {
	return a.httpHandler
}

// currentRulesVersion is the rules version new battles use, the analytics default. Every
// catalog scenario shares one ruleset; an empty catalog reports no default.
func currentRulesVersion(scenarios *catalog.Catalog) string {
	summaries := scenarios.Summaries()
	if len(summaries) == 0 {
		return ""
	}
	return summaries[0].RulesVersion
}

// StartLeagueScheduler plays league fixtures in the background until ctx ends or stop
// is called; stop waits for the current pass to finish.
func (a *App) StartLeagueScheduler(ctx context.Context) (stop func()) {
	schedulerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.leagues.RunScheduler(schedulerCtx, leagueservice.SchedulerInterval)
	}()
	return func() { cancel(); <-done }
}
