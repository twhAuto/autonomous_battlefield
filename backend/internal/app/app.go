package app

import (
	"net/http"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	battleapi "github.com/twh09/autonomous_battlefield/backend/internal/battle/api"
	"github.com/twh09/autonomous_battlefield/backend/internal/battle/catalog"
	battlerepo "github.com/twh09/autonomous_battlefield/backend/internal/battle/repo"
	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
	factionapi "github.com/twh09/autonomous_battlefield/backend/internal/faction/api"
	factionrepo "github.com/twh09/autonomous_battlefield/backend/internal/faction/repo"
	factionservice "github.com/twh09/autonomous_battlefield/backend/internal/faction/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/httpapi"
	"github.com/twh09/autonomous_battlefield/backend/internal/postgres"
	"github.com/twh09/autonomous_battlefield/backend/internal/telemetry"
)

// App is the application's composition root. It constructs and connects the
// repositories, services, and handlers used by the process.
type App struct {
	httpHandler http.Handler
}

func NewAPI(db postgres.DBTX, catalog *catalog.Catalog, maxTicks uint64, registries ...*ai.ProfileRegistry) *App {
	return NewAPIWithAuthentication(db, catalog, maxTicks, authn.Config{}, registries...)
}

func NewAPIWithAuthentication(db postgres.DBTX, catalog *catalog.Catalog, maxTicks uint64, authentication authn.Config, registries ...*ai.ProfileRegistry) *App {
	return NewAPIWithResultCache(db, catalog, maxTicks, authentication, nil, registries...)
}

func NewAPIWithResultCache(db postgres.DBTX, catalog *catalog.Catalog, maxTicks uint64, authentication authn.Config, cache battleservice.ResultCache, registries ...*ai.ProfileRegistry) *App {
	factionRepository := factionrepo.NewPostgres(db)
	factionService := factionservice.NewService(factionRepository, catalog)
	battleService := battleservice.NewServiceWithResultCache(battlerepo.NewPostgres(db), factionRepository, catalog, maxTicks, cache, registries...)

	factionHandler := factionapi.NewHandler(factionService)
	battleHandler := battleapi.NewHandler(battleService)
	authenticationManager := authn.NewManager(authentication)
	authenticationMiddleware := func(next http.Handler) http.Handler {
		return authn.RequireAuthentication(authentication.Enabled, authenticationManager, next)
	}
	router := httpapi.NewRouterWithMiddleware(
		[]httpapi.Middleware{authenticationMiddleware},
		authn.NewRoutes(authenticationManager), factionHandler, battleHandler,
	)
	// Tracing wraps access logging so completion logs share the request span.
	return &App{httpHandler: telemetry.HTTPHandler("battlefield-api.http", router)}
}

func (a *App) HTTPHandler() http.Handler {
	return a.httpHandler
}
