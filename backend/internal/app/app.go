package app

import (
	"context"
	"net/http"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	battlerepo "github.com/twh09/autonomous_battlefield/backend/internal/battle/repo"
	battleapi "github.com/twh09/autonomous_battlefield/backend/internal/battle/server/api"
	"github.com/twh09/autonomous_battlefield/backend/internal/battle/server/catalog"
	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/server/service"
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

type Database interface {
	postgres.DBTX
	Ping(context.Context) error
}

func NewAPI(db Database, catalog *catalog.Catalog, maxTicks uint64, registries ...*ai.ProfileRegistry) *App {
	return NewAPIWithAuthentication(db, catalog, maxTicks, authn.Config{}, registries...)
}

func NewAPIWithAuthentication(db Database, catalog *catalog.Catalog, maxTicks uint64, authentication authn.Config, registries ...*ai.ProfileRegistry) *App {
	factionRepository := factionrepo.NewPostgres(db)
	factionService := factionservice.NewService(factionRepository, catalog)
	battleService := battleservice.NewService(battlerepo.NewPostgres(db), factionRepository, catalog, maxTicks, registries...)

	factionHandler := factionapi.NewHandler(factionService)
	battleHandler := battleapi.NewHandler(battleService)
	readinessRoutes := httpapi.NewReadinessRoutes(db)
	authenticationManager := authn.NewManager(authentication)
	authenticationMiddleware := func(next http.Handler) http.Handler {
		return authn.RequireAuthentication(authentication.Enabled, authenticationManager, next)
	}
	router := httpapi.NewRouterWithMiddleware(
		[]httpapi.Middleware{func(next http.Handler) http.Handler { return telemetry.HTTPHandler("battlefield-api.http", next) }, authenticationMiddleware},
		readinessRoutes, authn.NewRoutes(authenticationManager), factionHandler, battleHandler,
	)
	return &App{httpHandler: router}
}

func (a *App) HTTPHandler() http.Handler {
	return a.httpHandler
}
