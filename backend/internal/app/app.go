// Composes API repositories, services, routes, and authentication middleware; process
// startup remains in cmd/server.

package app

import (
	"net/http"

	"github.com/twh09/autonomous_battlefield/backend/internal/admin"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/profile"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	battleapi "github.com/twh09/autonomous_battlefield/backend/internal/battle/api"
	"github.com/twh09/autonomous_battlefield/backend/internal/battle/catalog"
	battlerepo "github.com/twh09/autonomous_battlefield/backend/internal/battle/repo"
	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
	factionapi "github.com/twh09/autonomous_battlefield/backend/internal/faction/api"
	factionrepo "github.com/twh09/autonomous_battlefield/backend/internal/faction/repo"
	factionservice "github.com/twh09/autonomous_battlefield/backend/internal/faction/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/generation"
	generationrepo "github.com/twh09/autonomous_battlefield/backend/internal/generation/repo"
	"github.com/twh09/autonomous_battlefield/backend/internal/httpapi"
	"github.com/twh09/autonomous_battlefield/backend/internal/members"
	"github.com/twh09/autonomous_battlefield/backend/internal/postgres"
	"github.com/twh09/autonomous_battlefield/backend/internal/telemetry"
)

// App is the application's composition root. It constructs and connects the
// repositories, services, and handlers used by the process.
type App struct {
	httpHandler http.Handler
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
func New(db postgres.DBTX, catalog *catalog.Catalog, maxTicks uint64, options Options) *App {
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
	generationHandler := &generation.Handler{Repository: &generationrepo.Postgres{DB: db}, Config: generationConfig, Profiles: profiles}
	factionRepository := factionrepo.NewPostgres(db)
	factionService := factionservice.NewService(factionRepository, catalog)
	battleService := battleservice.NewService(battlerepo.NewPostgres(db), factionRepository, catalog, maxTicks, battleservice.Options{Cache: options.Cache, Profiles: profiles})

	factionHandler := factionapi.NewHandler(factionService)
	battleHandler := battleapi.NewHandler(battleService)
	authenticationManager := authn.NewManager(authentication)
	authenticationMiddleware := func(next http.Handler) http.Handler {
		return authn.BrowserProtection(authentication.Origin, authn.RequireAuthentication(authenticationManager.Enabled(), authenticationManager, authn.RolePreview(authn.RoutePolicy(next))))
	}
	router := httpapi.NewRouterWithMiddleware(
		[]httpapi.Middleware{authenticationMiddleware},
		authn.NewRoutes(authenticationManager), factionHandler, battleHandler, generationHandler, adminHandler, &members.Handler{Store: options.Members, Origin: authentication.Origin},
	)
	// Tracing wraps access logging so completion logs share the request span.
	return &App{httpHandler: telemetry.HTTPHandler("battlefield-api.http", router)}
}

func (a *App) HTTPHandler() http.Handler {
	return a.httpHandler
}
