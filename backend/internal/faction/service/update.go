package service

import (
	"context"
	"github.com/twh09/autonomous_battlefield/backend/internal/faction"
	"strings"
	"unicode/utf8"
)

type Updater interface {
	UpdateFaction(context.Context, CreateInput) error
}
type UpdateRepository interface {
	UpdateFaction(context.Context, faction.Faction) error
}

func (s *service) UpdateFaction(ctx context.Context, input CreateInput) error {
	input.Background = strings.TrimSpace(input.Background)
	input.Name, input.Doctrine = strings.TrimSpace(input.Name), strings.TrimSpace(input.Doctrine)
	if utf8.RuneCountInString(input.Background) > 1000 || !uuidPattern.MatchString(input.ID) || input.Name == "" || input.Doctrine == "" || utf8.RuneCountInString(input.Doctrine) > 4000 {
		return ErrInvalidFaction
	}
	repository, ok := s.repository.(UpdateRepository)
	if !ok {
		return ErrInvalidFaction
	}
	return repository.UpdateFaction(ctx, faction.Faction{ID: input.ID, Name: input.Name, Doctrine: input.Doctrine, Background: input.Background})
}
