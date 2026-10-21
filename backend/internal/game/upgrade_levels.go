// Validates optional upgrade tracks and ordered purchases while retaining legacy IDs.
package game

import (
	"errors"
	"fmt"
	"strings"
)

var errUpgradeChoiceConflict = errors.New("exclusive upgrade choice already selected")

func validateUpgradeLevels(upgrades []Upgrade) error {
	definitions := UpgradesByID(Ruleset{Upgrades: upgrades})
	seen := map[string]map[int]bool{}
	for _, u := range upgrades {
		if u.Level == 0 && u.Track == "" && u.RequiresUpgradeID == "" {
			continue // Original one-time upgrades have no level metadata.
		}
		if strings.TrimSpace(u.Track) == "" || u.Level < 1 || u.Level > 3 {
			return fmt.Errorf("%w: invalid upgrade track or level for %q", ErrInvalidRuleset, u.ID)
		}
		if seen[u.Track] == nil {
			seen[u.Track] = map[int]bool{}
		}
		if seen[u.Track][u.Level] {
			return fmt.Errorf("%w: duplicate level in track %q", ErrInvalidRuleset, u.Track)
		}
		seen[u.Track][u.Level] = true
		if u.Level == 1 {
			if u.RequiresUpgradeID != "" {
				return fmt.Errorf("%w: first upgrade level cannot have a prerequisite", ErrInvalidRuleset)
			}
			continue
		}
		previous, ok := definitions[u.RequiresUpgradeID]
		if !ok || previous.Track != u.Track || previous.Level != u.Level-1 || previous.Cost >= u.Cost {
			return fmt.Errorf("%w: upgrade %q requires the preceding level and a higher price", ErrInvalidRuleset, u.ID)
		}
	}
	return nil
}

// ValidateUpgradeOrder checks prerequisites against owned levels and earlier orders.
// It does not simulate affordability: spending stops at the first unaffordable
// purchase, and a later production window may retry the same ordered plan.
func ValidateUpgradeOrder(upgrades []Upgrade, owned, orders []string) error {
	definitions := UpgradesByID(Ruleset{Upgrades: upgrades})
	available := map[string]bool{}
	groups := map[string]string{}
	for _, id := range owned {
		available[id] = true
		if group := definitions[id].ExclusiveGroup; group != "" {
			groups[group] = id
		}
	}
	for _, id := range orders {
		u, ok := definitions[id]
		if !ok {
			return fmt.Errorf("unsupported upgrade %q", id)
		}
		if available[id] {
			continue
		}
		if chosen := groups[u.ExclusiveGroup]; u.ExclusiveGroup != "" && chosen != "" && chosen != id {
			return fmt.Errorf("%w: upgrade %q conflicts with owned or earlier choice %q", errUpgradeChoiceConflict, id, chosen)
		}
		if u.RequiresUpgradeID != "" && !available[u.RequiresUpgradeID] {
			return fmt.Errorf("upgrade %q requires %q owned or ordered earlier", id, u.RequiresUpgradeID)
		}
		available[id] = true
		if group := definitions[id].ExclusiveGroup; group != "" {
			groups[group] = id
		}
	}
	return nil
}
