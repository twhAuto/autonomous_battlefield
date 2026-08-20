package service

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/twh09/autonomous_battlefield/backend/internal/faction"
)

var (
	ErrFactionNotFound      = errors.New("faction not found")
	ErrFactionAlreadyExists = errors.New("faction already exists")
	ErrInvalidFaction       = errors.New("invalid faction")
)

var uuidPattern = regexp.MustCompile(`^[[:xdigit:]]{8}-[[:xdigit:]]{4}-[[:xdigit:]]{4}-[[:xdigit:]]{4}-[[:xdigit:]]{12}$`)

type CreateInput struct {
	ID   string
	Name string
}

type Service interface {
	GetFactionName(ctx context.Context, id string) (string, error)
	ListFactions(ctx context.Context) ([]faction.Faction, error)
	CreateFaction(ctx context.Context, input CreateInput) error
}

type service struct {
	repository Repository
}

func (s *service) GetFactionName(ctx context.Context, id string) (string, error) {
	faction, err := s.repository.GetFaction(ctx, id)
	if err != nil {
		return "", err
	}
	return faction.Name, nil
}

func (s *service) ListFactions(ctx context.Context) ([]faction.Faction, error) {
	return s.repository.ListFactions(ctx)
}

func (s *service) CreateFaction(ctx context.Context, input CreateInput) error {
	if !uuidPattern.MatchString(input.ID) || strings.TrimSpace(input.Name) == "" {
		return ErrInvalidFaction
	}

	storedFaction := faction.Faction{
		ID:   input.ID,
		Name: strings.TrimSpace(input.Name),
	}
	if err := s.repository.CreateFaction(ctx, storedFaction); err != nil {
		return err
	}

	return nil
}

func NewService(repository Repository) Service {
	return &service{repository: repository}
}
