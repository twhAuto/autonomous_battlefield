// Adds profile editing through optional update contracts, reusing the same normalization
// as faction creation.

package service

import (
	"context"

	"github.com/twh09/autonomous_battlefield/backend/internal/faction"
)

type Updater interface {
	UpdateFaction(context.Context, CreateInput) error
}
type UpdateRepository interface {
	UpdateFaction(context.Context, faction.Faction) error
}

func (s *service) UpdateFaction(ctx context.Context, input CreateInput) error {
	stored, err := normalizeFaction(input)
	if err != nil {
		return err
	}

	repository, ok := s.repository.(UpdateRepository)
	if !ok {
		return ErrInvalidFaction
	}
	return repository.UpdateFaction(ctx, stored)
}
