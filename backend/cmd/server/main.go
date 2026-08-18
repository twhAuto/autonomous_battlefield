package main

import (
	"fmt"

	"github.com/twh09/autonomous_battlefield/backend/internal/api"
	"github.com/twh09/autonomous_battlefield/backend/internal/repository"
	"github.com/twh09/autonomous_battlefield/backend/internal/service"
)

func main() {
	fmt.Println("Autonomous Battlefield is starting...")

	startServer()
	//handlers := api.NewBattlefieldHandler()
}

func startServer() {
	// Initialize the repository
	factionRepo := repository.NewFactionRepo()

	// Initialize the service with the repository
	factionService := service.NewFactionService(factionRepo)

	// Initialize the handler with the service
	factionHandler := api.NewFactionHandler(factionService)

	// Start the server and pass the handler to handle requests
	// For example, you can use an HTTP server or any other server framework to listen for incoming requests
	// and route them to the appropriate handler methods.
	// Example: http.ListenAndServe(":8080", factionHandler)
}
