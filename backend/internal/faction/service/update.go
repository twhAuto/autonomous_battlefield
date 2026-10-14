// Edits faction profiles under the same normalization as creation; only the owner or
// an Admin may edit.

package service

import "context"

func (s *service) UpdateFaction(ctx context.Context, input ProfileInput) error {
	stored, err := normalizeFaction(input)
	if err != nil {
		return err
	}
	if err := s.authorize(ctx, stored.ID); err != nil {
		return err
	}
	return s.repository.UpdateFaction(ctx, stored)
}
