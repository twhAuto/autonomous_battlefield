package game

import (
	"fmt"
	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
)

// DecisionWindowDue reports whether the next transition is a production and
// strategy boundary. The observation is taken from the current frozen state.
func (m *Match) DecisionWindowDue() bool {
	return !m.finished && m.scenario.DecisionIntervalTicks > 0 && (m.state.Tick+1)%uint64(m.scenario.DecisionIntervalTicks) == 0
}

func (m *Match) DecisionTargetTick() uint64 { return m.state.Tick + 1 }

func (m *Match) DecisionSnapshot(factionID string) (DecisionSnapshot, error) {
	runtime := m.factions[factionID]
	if runtime == nil {
		return DecisionSnapshot{}, fmt.Errorf("unknown faction %q", factionID)
	}
	view, err := m.View(factionID)
	if err != nil {
		return DecisionSnapshot{}, err
	}
	castle := m.castle(factionID)
	projectedIncome := int64(0)
	if castle != nil {
		projectedIncome = m.incomeFor(factionID, castle.Income)
	}
	legalSites := make([]controller.Coordinate, 0, 2)
	for slot, setup := range m.scenario.Factions {
		if setup.ID == factionID {
			for _, site := range m.scenario.Map.EconomySites[slot] {
				legalSites = append(legalSites, controller.Coordinate{X: site.X, Y: site.Y})
			}
		}
	}
	return DecisionSnapshot{BattleID: m.id, FactionID: factionID, TargetTick: m.DecisionTargetTick(), Version: m.state.Tick,
		View: view, Gold: m.factionState(factionID).Gold, ProjectedIncome: projectedIncome,
		EconomyCost: m.scenario.Ruleset.Buildings.EconomyCost, EconomyIncome: m.scenario.Ruleset.Buildings.EconomyIncome + runtime.economyBonus,
		EconomyCount: m.economyCount(factionID), MaximumEconomy: m.scenario.Ruleset.Buildings.MaximumEconomyCount,
		LegalUnits: append([]UnitType(nil), m.scenario.Ruleset.Units...), LegalSites: legalSites,
		ActivePlan: controller.ClonePlanProposal(runtime.activePlan)}, nil
}

// ScriptedPlans resolves deterministic plans without exposing authoritative
// state mutation to the scripted controller path.
func (m *Match) ScriptedPlans() map[string]controller.PlanProposal {
	plans := make(map[string]controller.PlanProposal, len(m.factions))
	for _, factionID := range m.sortedFactionIDs() {
		plans[factionID] = m.normalizedFactionPlan(factionID, m.factions[factionID].scriptedPlan())
	}
	return plans
}

func (m *Match) applyPlans(plans map[string]controller.PlanProposal) error {
	for factionID, runtime := range m.factions {
		runtime.activePlan = m.normalizedFactionPlan(factionID, runtime.activePlan)
	}
	for factionID, proposal := range plans {
		runtime := m.factions[factionID]
		if runtime == nil {
			return fmt.Errorf("unknown faction plan %q", factionID)
		}
		proposal = m.normalizedFactionPlan(factionID, proposal)
		if err := validateProductionPlan(m.scenario, runtime.setup, proposal); err != nil {
			return err
		}
		runtime.activePlan = controller.ClonePlanProposal(proposal)
		runtime.activeSource = "accepted"
	}
	return nil
}

func (m *Match) selectTactics() (map[string]string, []Event, error) {
	tactics := make(map[string]string, 2)
	events := make([]Event, 0, 2)
	for _, factionID := range m.sortedFactionIDs() {
		runtime := m.factions[factionID]
		plan := runtime.activePlan
		attackWeight := plan.ActionWeights[ActionAttack]
		if attackWeight == 0 {
			attackWeight = 1000
		}
		defendWeight := plan.ActionWeights[ActionDefend]
		if defendWeight == 0 {
			defendWeight = 1000
		}
		attackScore := m.projectedPower(factionID)*attackWeight + plan.TargetBonuses[m.opponentID(factionID)]
		defendScore := m.projectedPower(m.opponentID(factionID))*defendWeight + plan.TargetBonuses[factionID]
		tactic := ActionAttack
		if defendScore > attackScore {
			tactic = ActionDefend
		}
		tactics[factionID] = tactic
		if tactic == ActionDefend {
			runtime.defendTicks++
		} else {
			runtime.attackTicks++
		}
		events = append(events, m.event("tactic_selected", map[string]any{"factionId": factionID, "tactic": tactic, "planName": plan.Name, "source": runtime.activeSource}))
	}
	return tactics, events, nil
}

func (f *factionRuntime) scriptedPlan() controller.PlanProposal {
	index := f.planIndex
	if index >= len(f.setup.ScriptedPlans) {
		index = len(f.setup.ScriptedPlans) - 1
	}
	f.planIndex++
	return controller.ClonePlanProposal(f.setup.ScriptedPlans[index])
}

func normalizedPlan(value controller.PlanProposal, setup FactionSetup) controller.PlanProposal {
	result := controller.ClonePlanProposal(value)
	if len(result.SpendOrder) == 0 {
		result.SpendOrder = []string{"construction", "recruitment"}
	}
	if len(result.RecruitmentPriorities) == 0 {
		result.RecruitmentPriorities = append([]string(nil), setup.Blueprint.RecruitmentPriority...)
	}
	if len(result.ConstructionPriorities) == 0 {
		// The owning slot is resolved by matching the faction when the plan is
		// applied; scenario defaults are added in Match.normalizedFactionPlan.
		result.ConstructionPriorities = nil
	}
	return result
}

func (m *Match) normalizedFactionPlan(factionID string, value controller.PlanProposal) controller.PlanProposal {
	runtime := m.factions[factionID]
	result := normalizedPlan(value, runtime.setup)
	if len(result.ConstructionPriorities) == 0 {
		for slot, setup := range m.scenario.Factions {
			if setup.ID != factionID {
				continue
			}
			for _, site := range m.scenario.Map.EconomySites[slot] {
				result.ConstructionPriorities = append(result.ConstructionPriorities, controller.Coordinate{X: site.X, Y: site.Y})
			}
		}
	}
	return result
}

func validateProductionPlan(scenario Scenario, setup FactionSetup, plan controller.PlanProposal) error {
	if err := controller.ValidatePlanProposal(plan, []string{ActionAttack, ActionDefend}); err != nil {
		return err
	}
	units := UnitTypesByID(scenario.Ruleset)
	for _, unitType := range plan.RecruitmentPriorities {
		if _, ok := units[unitType]; !ok {
			return fmt.Errorf("%w: unsupported recruitment unit %q", controller.ErrInvalidPlan, unitType)
		}
	}
	allowedSites := make(map[Position]bool)
	for slot, faction := range scenario.Factions {
		if faction.ID == setup.ID {
			for _, site := range scenario.Map.EconomySites[slot] {
				allowedSites[site] = true
			}
		}
	}
	for _, coordinate := range plan.ConstructionPriorities {
		if !allowedSites[Position{X: coordinate.X, Y: coordinate.Y}] {
			return fmt.Errorf("%w: construction site (%d,%d) is not legal", controller.ErrInvalidPlan, coordinate.X, coordinate.Y)
		}
	}
	return nil
}
