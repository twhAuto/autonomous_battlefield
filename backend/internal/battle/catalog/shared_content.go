// Resolves library entries and validates shared controller plans against current content.

package catalog

import (
	"fmt"

	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
)

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
	if game.EconomyAvailable(r.Buildings) {
		ids = append(ids, economy)
	}
	for _, b := range r.Structures {
		ids = append(ids, b.ID)
	}
	return ids
}
func validateSelectedPlans(t template, r game.Ruleset) error {
	for _, p := range append([]controller.PlanProposal{t.FallbackPlan}, t.ScriptedPlans...) {
		if err := validateSelectedPlan(p, r); err != nil {
			return err
		}
	}
	return nil
}

func validateSelectedPlan(p controller.PlanProposal, r game.Ruleset) error {
	if err := game.ValidateUpgradeOrder(r.Upgrades, nil, p.UpgradeOrders); err != nil {
		return fmt.Errorf("plan %q: %w", p.Name, err)
	}
	units := game.UnitTypesByID(r)
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
	if err := validateSelectedConstruction(p, r); err != nil {
		return err
	}
	if !game.EconomyAvailable(r.Buildings) && len(p.ConstructionPriorities) > 0 {
		return fmt.Errorf("plan %q builds excluded economy buildings", p.Name)
	}
	return nil
}

func validateSelectedConstruction(p controller.PlanProposal, r game.Ruleset) error {
	if p.ConstructionOrders != nil {
		legal := map[string]bool{}
		for _, b := range game.ConstructibleBuildings(r) {
			legal[b.ID] = true
		}
		for _, order := range *p.ConstructionOrders {
			if !legal[order.BuildingType] {
				return fmt.Errorf("plan %q builds excluded type %q", p.Name, order.BuildingType)
			}
		}
	}
	return nil
}
