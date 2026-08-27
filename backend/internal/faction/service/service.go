package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

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
	ID        string
	Name      string
	RulesetID string
	Blueprint game.Blueprint
}

type Service interface {
	GetFactionName(ctx context.Context, id string) (string, error)
	ListFactions(ctx context.Context) ([]faction.Faction, error)
	CreateFaction(ctx context.Context, input CreateInput) error
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
	for index := range values {
		s.decorate(&values[index])
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
	if s.catalog != nil {
		ruleset, ok := s.catalog.Ruleset(input.RulesetID)
		if !ok {
			return fmt.Errorf("%w: unknown ruleset %q", ErrInvalidBlueprint, input.RulesetID)
		}
		if _, err := game.ValidateBlueprint(ruleset, input.Blueprint); err != nil {
			return err
		}
	}

	storedFaction := faction.Faction{
		ID: input.ID, Name: strings.TrimSpace(input.Name), RulesetID: input.RulesetID,
		Blueprint: game.CloneBlueprint(input.Blueprint),
	}
	if err := s.repository.CreateFaction(ctx, storedFaction); err != nil {
		return err
	}

	return nil
}

func (s *service) decorate(value *faction.Faction) {
	if s.catalog == nil {
		return
	}
	ruleset, ok := s.catalog.Ruleset(value.RulesetID)
	if !ok {
		return
	}
	value.SpentPoints = game.DesignCost(ruleset, value.Blueprint)
	value.Remaining = ruleset.DesignBudget - value.SpentPoints
}

func NewService(repository Repository, catalogs ...RulesetCatalog) Service {
	var catalog RulesetCatalog
	if len(catalogs) > 0 {
		catalog = catalogs[0]
	}
	return &service{repository: repository, catalog: catalog}
}
