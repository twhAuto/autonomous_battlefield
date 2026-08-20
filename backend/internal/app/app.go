package app

import (
	"net/http"

	"github.com/twh09/autonomous_battlefield/backend/internal/api"
	"github.com/twh09/autonomous_battlefield/backend/internal/database"
	"github.com/twh09/autonomous_battlefield/backend/internal/repository"
	"github.com/twh09/autonomous_battlefield/backend/internal/service"
)

// App is the application's composition root. It constructs and connects the
// repositories, services, and handlers used by the process.
type App struct {
	httpHandler http.Handler
}

func New(db database.DBTX) *App {
	factionRepo := repository.NewFactionRepo(db)
	factionService := service.NewFactionService(factionRepo)

	factionHandler := api.NewFactionHandler(factionService)

	return &App{httpHandler: api.NewRouter(factionHandler)}
}

func (a *App) HTTPHandler() http.Handler {
	return a.httpHandler
}
