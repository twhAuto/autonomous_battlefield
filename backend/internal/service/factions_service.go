package service

import (
	"context"

	"github.com/twh09/autonomous_battlefield/backend/internal/repository"
)

type factionService struct {
	repo repository.FactionRepository
}

type FactionService interface {
	GetFactionName(ctx context.Context, id string) (string, error)
}

func (b *factionService) GetFactionName(ctx context.Context, id string) (string, error) {
	faction, err := b.repo.GetFaction(ctx, id)
	if err != nil {
		return "", err
	}
	return faction.Name, nil
}

func NewFactionService(repo repository.FactionRepository) FactionService {
	return &factionService{
		repo: repo,
	}
}
