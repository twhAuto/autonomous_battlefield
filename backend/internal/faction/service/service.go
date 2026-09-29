// Normalizes faction profiles, enforces text and identifier rules, and coordinates
// repository and ruleset-catalog access.

package service

import (
	"context"
	"errors"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
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
	return s.repository.ListFactions(ctx)
}

func (s *service) ListRulesets() []game.Ruleset {
	if s.catalog == nil {
		return []game.Ruleset{}
	}
	return s.catalog.Rulesets()
}

func (s *service) CreateFaction(ctx context.Context, input CreateInput) error {
	stored, err := normalizeFaction(input)
	if err != nil {
		return err
	}
	err = s.repository.CreateFaction(ctx, stored)
	if errors.Is(err, ErrFactionAlreadyExists) {
		// A retry of the same caller's identical request succeeds, matching battle
		// creation; any other reuse of the client-generated ID remains a conflict.
		existing, getErr := s.repository.GetFaction(ctx, stored.ID)
		if getErr != nil && !errors.Is(getErr, ErrFactionNotFound) {
			// An unverified retry must surface as a server error, not a conflict.
			return getErr
		}
		if getErr == nil && existing != nil && existing.OwnerID == authn.Owner(ctx) && existing.Name == stored.Name &&
			existing.Doctrine == stored.Doctrine && existing.Background == stored.Background {
			return nil
		}
	}
	return err
}

// normalizeFaction keeps creation and editing subject to the same profile rules.
func normalizeFaction(input CreateInput) (faction.Faction, error) {
	stored := faction.Faction{
		ID:         input.ID,
		Name:       strings.TrimSpace(input.Name),
		Doctrine:   strings.TrimSpace(input.Doctrine),
		Background: strings.TrimSpace(input.Background),
	}
	if !uuidPattern.MatchString(stored.ID) || stored.Name == "" || stored.Doctrine == "" ||
		utf8.RuneCountInString(stored.Doctrine) > faction.MaxDoctrineLength || utf8.RuneCountInString(stored.Background) > faction.MaxBackgroundLength {
		return faction.Faction{}, ErrInvalidFaction
	}
	return stored, nil
}

func (s *service) DeleteFaction(ctx context.Context, id string) error {
	if !uuidPattern.MatchString(id) {
		return ErrInvalidFaction
	}
	if err := s.authorize(ctx, id); err != nil {
		return err
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

func (s *service) authorize(ctx context.Context, id string) error {
	a, ok := authn.ActorFromContext(ctx)
	if !ok {
		return authn.ErrForbidden
	}
	if a.Role == authn.Admin {
		return nil
	}
	stored, err := s.repository.GetFaction(ctx, id)
	if err != nil {
		return err
	}
	return authn.AuthorizeOwner(ctx, stored.OwnerID)
}
