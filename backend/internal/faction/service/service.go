package service

import (
	"context"
	"errors"

	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/twh09/autonomous_battlefield/backend/internal/faction"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
)

var (
	ErrFactionNotFound      = errors.New("faction not found")
	ErrFactionAlreadyExists = errors.New("faction already exists")
	ErrInvalidFaction       = errors.New("invalid faction")
	ErrInvalidBlueprint     = game.ErrInvalidBlueprint
)

var uuidPattern = regexp.MustCompile(`^[[:xdigit:]]{8}-[[:xdigit:]]{4}-[[:xdigit:]]{4}-[[:xdigit:]]{4}-[[:xdigit:]]{12}$`)

type CreateInput struct {
	Background string
	Doctrine   string
	ID         string
	Name       string
}

type Service interface {
	GetFactionName(ctx context.Context, id string) (string, error)
	ListFactions(ctx context.Context) ([]faction.Faction, error)
	CreateFaction(ctx context.Context, input CreateInput) error
	DeleteFaction(ctx context.Context, id string) error
}

type RulesetCatalog interface {
	Ruleset(string) (game.Ruleset, bool)
	Rulesets() []game.Ruleset
}

type RulesetLister interface {
	ListRulesets() []game.Ruleset
}

type service struct {
	repository Repository
	catalog    RulesetCatalog
}

func (s *service) GetFactionName(ctx context.Context, id string) (string, error) {
	faction, err := s.repository.GetFaction(ctx, id)
	if err != nil {
		return "", err
	}
	return faction.Name, nil
}

func (s *service) ListFactions(ctx context.Context) ([]faction.Faction, error) {
	values, err := s.repository.ListFactions(ctx)
	if err != nil {
		return nil, err
	}
	return values, nil
}

func (s *service) ListRulesets() []game.Ruleset {
	if s.catalog == nil {
		return []game.Ruleset{}
	}
	return s.catalog.Rulesets()
}

func (s *service) CreateFaction(ctx context.Context, input CreateInput) error {
	if !uuidPattern.MatchString(input.ID) || strings.TrimSpace(input.Name) == "" {
		return ErrInvalidFaction
	}
	if utf8.RuneCountInString(strings.TrimSpace(input.Background)) > 1000 || strings.TrimSpace(input.Doctrine) == "" || utf8.RuneCountInString(strings.TrimSpace(input.Doctrine)) > 4000 {
		return ErrInvalidFaction
	}

	storedFaction := faction.Faction{
		ID: input.ID, Name: strings.TrimSpace(input.Name), Doctrine: strings.TrimSpace(input.Doctrine), Background: strings.TrimSpace(input.Background),
	}
	if err := s.repository.CreateFaction(ctx, storedFaction); err != nil {
		return err
	}

	return nil
}

func (s *service) DeleteFaction(ctx context.Context, id string) error {
	if !uuidPattern.MatchString(id) {
		return ErrInvalidFaction
	}
	return s.repository.DeleteFaction(ctx, id)
}

func NewService(repository Repository, catalogs ...RulesetCatalog) Service {
	var catalog RulesetCatalog
	if len(catalogs) > 0 {
		catalog = catalogs[0]
	}
	return &service{repository: repository, catalog: catalog}
}
