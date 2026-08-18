package api

import (
	"context"

	"github.com/twh09/autonomous_battlefield/backend/internal/service"
)

type FactionHandler struct {
	FactionService service.FactionService
}

func (h *FactionHandler) GetFactionName(ctx context.Context, id string) (string, error) {
	return h.FactionService.GetFactionName(ctx, id)
}

func NewFactionHandler(service service.FactionService) *FactionHandler {
	return &FactionHandler{
		FactionService: service, // Pass the appropriate repository implementation here
	}
}
