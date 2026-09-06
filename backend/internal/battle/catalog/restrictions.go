package catalog

import (
	"fmt"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
)

func selectEntries[T any](entries []T, ids []string, idOf func(T) string) ([]T, error) {
	if ids == nil {
		return append([]T{}, entries...), nil
	}
	byID := map[string]T{}
	for _, entry := range entries {
		byID[idOf(entry)] = entry
	}
	result := []T{}
	seen := map[string]bool{}
	for _, id := range ids {
		entry, ok := byID[id]
		if !ok || seen[id] {
			return nil, fmt.Errorf("unknown or duplicate selected content %q", id)
		}
		seen[id] = true
		result = append(result, entry)
	}
	return result, nil
}
func selectUnits(entries []game.UnitType, ids []string) ([]game.UnitType, error) {
	return selectEntries(entries, ids, func(u game.UnitType) string { return u.ID })
}
func selectUpgrades(entries []game.Upgrade, ids []string) ([]game.Upgrade, error) {
	return selectEntries(entries, ids, func(u game.Upgrade) string { return u.ID })
}
func selectedKnowledge(k *game.RulesKnowledge, units []game.UnitType) *game.RulesKnowledge {
	if k == nil {
		return nil
	}
	result := game.CloneKnowledge(k)
	result.Cards = []game.UnitCard{}
	cards := map[string]game.UnitCard{}
	for _, c := range k.Cards {
		cards[c.UnitID] = c
	}
	for _, u := range units {
		result.Cards = append(result.Cards, cards[u.ID])
	}
	return result
}
func unitIDs(r game.Ruleset) []string {
	ids := []string{}
	for _, u := range r.Units {
		ids = append(ids, u.ID)
	}
	return ids
}
func upgradeIDs(r game.Ruleset) []string {
	ids := []string{}
	for _, u := range r.Upgrades {
		ids = append(ids, u.ID)
	}
	return ids
}
func buildingIDs(r game.Ruleset) []string {
	castle, economy := r.Buildings.CastleTypeID, r.Buildings.EconomyTypeID
	if castle == "" {
		castle = "castle"
	}
	if economy == "" {
		economy = "economy"
	}
	ids := []string{castle}
	if r.Buildings.MaximumEconomyCount > 0 {
		ids = append(ids, economy)
	}
	return ids
}
func restrictRuleset(base game.Ruleset, t template) (game.Ruleset, error) {
	r := cloneRuleset(base)
	var err error
	r.Units, err = selectUnits(base.Units, t.AllowedUnits)
	if err != nil {
		return r, err
	}
	r.Upgrades, err = selectUpgrades(base.Upgrades, t.AllowedUpgrades)
	if err != nil {
		return r, err
	}
	r.Knowledge = selectedKnowledge(base.Knowledge, r.Units)
	if t.AllowedBuildings != nil {
		available := buildingIDs(base)
		selected, err := selectEntries(available, t.AllowedBuildings, func(s string) string { return s })
		if err != nil {
			return r, err
		}
		castleIncluded := false
		for _, id := range selected {
			if id == available[0] {
				castleIncluded = true
			}
		}
		if !castleIncluded {
			return r, fmt.Errorf("scenario must include required castle building %q", available[0])
		}
		if len(selected) == 1 {
			r.Buildings.MaximumEconomyCount = 0
		}
	}
	if err := game.ValidateRuleset(r); err != nil {
		return r, err
	}
	return r, nil
}
func validateSelectedPlans(t template, r game.Ruleset) error {
	units := game.UnitTypesByID(r)
	for _, p := range append([]controller.PlanProposal{t.FallbackPlan}, t.ScriptedPlans...) {
		ids := append([]string{}, p.RecruitmentPriorities...)
		if p.RecruitmentOrders != nil {
			for _, order := range *p.RecruitmentOrders {
				ids = append(ids, order.UnitType)
			}
		}
		for _, id := range ids {
			if _, ok := units[id]; !ok {
				return fmt.Errorf("plan %q recruits excluded unit %q", p.Name, id)
			}
		}
		if r.Buildings.MaximumEconomyCount == 0 && len(p.ConstructionPriorities) > 0 {
			return fmt.Errorf("plan %q builds excluded economy buildings", p.Name)
		}
	}
	return nil
}
