// Validates rules knowledge against the ruleset and copies knowledge cards for isolated
// snapshots.

package game

import (
	"fmt"
	"strings"
)

func CloneKnowledge(k *RulesKnowledge) *RulesKnowledge {
	if k == nil {
		return nil
	}
	copy := *k
	copy.Cards = append([]UnitCard(nil), k.Cards...)
	return &copy
}
func ValidateKnowledge(r Ruleset) error {
	if r.Knowledge == nil {
		return nil
	}
	if strings.TrimSpace(r.Knowledge.Mechanics) == "" || len(r.Knowledge.Mechanics) > 4096 {
		return fmt.Errorf("invalid knowledge mechanics")
	}
	units := UnitTypesByID(r)
	seen := map[string]bool{}
	for _, c := range r.Knowledge.Cards {
		if _, ok := units[c.UnitID]; !ok || seen[c.UnitID] {
			return fmt.Errorf("invalid knowledge unit %q", c.UnitID)
		}
		seen[c.UnitID] = true
		for _, text := range []string{c.Role, c.Strengths, c.Weaknesses, c.Guidance} {
			if strings.TrimSpace(text) == "" || len(text) > 1024 {
				return fmt.Errorf("invalid knowledge text for %q", c.UnitID)
			}
		}
	}
	if len(seen) != len(units) {
		return fmt.Errorf("knowledge requires one card per unit")
	}
	return nil
}
