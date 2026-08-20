package service

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/twh09/autonomous_battlefield/backend/internal/repository"
	"github.com/twh09/autonomous_battlefield/backend/internal/service/entity"
)

var (
	ErrFactionNotFound      = errors.New("faction not found")
	ErrFactionAlreadyExists = errors.New("faction already exists")
	ErrInvalidFaction       = errors.New("invalid faction")
)

var uuidPattern = regexp.MustCompile(`^[[:xdigit:]]{8}-[[:xdigit:]]{4}-[[:xdigit:]]{4}-[[:xdigit:]]{4}-[[:xdigit:]]{12}$`)

type factionService struct {
	repo repository.FactionRepository
}

type FactionService interface {
	GetFactionName(ctx context.Context, id string) (string, error)
	ListFactions(ctx context.Context) ([]entity.FactionEntity, error)
	CreateFaction(ctx context.Context, faction *entity.FactionEntity) error
}

func (b *factionService) GetFactionName(ctx context.Context, id string) (string, error) {
	faction, err := b.repo.GetFaction(ctx, id)
	if errors.Is(err, repository.ErrFactionNotFound) {
		return "", ErrFactionNotFound
	}
	if err != nil {
		return "", err
	}
	return faction.Name, nil
}

func (b *factionService) ListFactions(ctx context.Context) ([]entity.FactionEntity, error) {
	return b.repo.ListFactions(ctx)
}

func (b *factionService) CreateFaction(ctx context.Context, faction *entity.FactionEntity) error {
	if faction == nil || !uuidPattern.MatchString(faction.ID) || strings.TrimSpace(faction.Name) == "" {
		return ErrInvalidFaction
	}

	storedFaction := &entity.FactionEntity{
		ID:   faction.ID,
		Name: strings.TrimSpace(faction.Name),
	}
	if err := b.repo.CreateFaction(ctx, storedFaction); errors.Is(err, repository.ErrFactionAlreadyExists) {
		return ErrFactionAlreadyExists
	} else if err != nil {
		return err
	}

	return nil
}

func NewFactionService(repo repository.FactionRepository) FactionService {
	return &factionService{
		repo: repo,
	}
}
