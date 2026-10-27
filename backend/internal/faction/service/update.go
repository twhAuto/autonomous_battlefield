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
	// An older client omits the emblem. Let the repository preserve it atomically.
	if input.Emblem == "" {
		stored.Emblem = ""
	}
	return s.repository.UpdateFaction(ctx, stored)
}
