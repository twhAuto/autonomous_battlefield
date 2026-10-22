// Builds decision snapshots, validates and normalizes submitted production plans, and
// selects per-faction tactics.

package game

import (
	"fmt"

	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
)

// DecisionWindowDue reports whether the next transition is a production and
// strategy boundary. The observation is taken from the current frozen state.
func (m *Match) DecisionWindowDue() bool {
	return m.OpeningDue() || !m.finished && m.scenario.DecisionIntervalTicks > 0 && (m.state.Tick+1)%uint64(m.scenario.DecisionIntervalTicks) == 0
}

func (m *Match) DecisionTargetTick() uint64 {
	if m.OpeningDue() {
		return 0
	}
	return m.state.Tick + 1
}

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
	legalSites := m.legalConstructionSites(factionID)
	if m.OpeningDue() {
		projectedIncome = 0
		legalSites = nil
	}
	upgrades := []Upgrade{}
	if m.scenario.SnapshotVersion >= DoctrineSnapshotVersion {
		upgrades = append(upgrades, m.scenario.Ruleset.Upgrades...)
	}
	return DecisionSnapshot{LegalBuildings: ConstructibleBuildings(m.scenario.Ruleset), RecruitmentSources: m.recruitmentSources(factionID), MaximumBuildings: m.scenario.Ruleset.Buildings.MaximumBuildingCount, Doctrine: runtime.setup.Doctrine, FactionName: runtime.setup.Name, Background: runtime.setup.Background, Opening: m.OpeningDue(), LegalUpgrades: upgrades, OwnedUpgradeIDs: append([]string{}, m.factionState(factionID).OwnedUpgradeIDs...), BattleID: m.id, FactionID: factionID, TargetTick: m.DecisionTargetTick(), Version: m.state.Tick,
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
		if m.OpeningDue() {
			plans[factionID] = m.OpeningFallback(factionID)
		} else {
			plan := m.normalizedFactionPlan(factionID, m.factions[factionID].scriptedPlan())
			m.assignForwardRecruitment(factionID, &plan)
			plans[factionID] = plan
		}
	}
	return plans
}

func (m *Match) applyPlans(plans map[string]controller.PlanProposal) error {
	for factionID, proposal := range plans {
		runtime := m.factions[factionID]
		if runtime == nil {
			return fmt.Errorf("unknown faction plan %q", factionID)
		}
		proposal = m.normalizedFactionPlan(factionID, proposal)
		if err := validateProductionPlan(m.scenario, runtime.setup, proposal); err != nil {
			return err
		}
		if err := ValidateUpgradeOrder(m.scenario.Ruleset.Upgrades, m.factionState(factionID).OwnedUpgradeIDs, proposal.UpgradeOrders); err != nil {
			return fmt.Errorf("%w: %v", controller.ErrInvalidPlan, err)
		}
	}
	for factionID, runtime := range m.factions {
		runtime.activePlan = m.normalizedFactionPlan(factionID, runtime.activePlan)
	}
	for factionID, proposal := range plans {
		runtime := m.factions[factionID]
		runtime.activePlan = m.normalizedFactionPlan(factionID, proposal)
		runtime.activeSource = "accepted"
	}

	return nil
}

func (m *Match) selectTactics() (map[string]string, []Event) {
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
	return tactics, events
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
	if result.RecruitmentOrders == nil && len(result.RecruitmentPriorities) == 0 {
		result.RecruitmentPriorities = append([]string(nil), setup.Blueprint.RecruitmentPriority...)
	}
	// Construction defaults depend on the faction's slot; normalizedFactionPlan adds them.
	return result
}

func (m *Match) normalizedFactionPlan(factionID string, value controller.PlanProposal) controller.PlanProposal {
	runtime := m.factions[factionID]
	result := normalizedPlan(value, runtime.setup)
	if m.scenario.SnapshotVersion >= DoctrineSnapshotVersion && result.RecruitmentOrders == nil && len(result.RecruitmentPriorities) == 0 {
		orders := m.OpeningFallback(factionID).RecruitmentOrders
		result.RecruitmentOrders = orders
	}
	if result.ConstructionOrders == nil && len(result.ConstructionPriorities) == 0 && !m.OpeningDue() && m.scenario.Ruleset.Buildings.MaximumEconomyCount > 0 {
		for slot, setup := range m.scenario.Factions {
			if setup.ID != factionID {
				continue
			}
			orders := m.defaultConstructionOrders(slot)
			result.ConstructionOrders = &orders
		}
	}
	return result
}

func validateProductionPlan(scenario Scenario, setup FactionSetup, plan controller.PlanProposal) error {
	if err := controller.ValidatePlanProposal(plan, []string{ActionAttack, ActionDefend}); err != nil {
		return err
	}
	if len(plan.UpgradeOrders) > 0 && scenario.SnapshotVersion < DoctrineSnapshotVersion {
		return fmt.Errorf("%w: upgrades require a doctrine battle", controller.ErrInvalidPlan)
	}
	upgrades := UpgradesByID(scenario.Ruleset)
	for _, id := range plan.UpgradeOrders {
		if _, ok := upgrades[id]; !ok {
			return fmt.Errorf("%w: unsupported upgrade %q", controller.ErrInvalidPlan, id)
		}
	}
	units := UnitTypesByID(scenario.Ruleset)
	if plan.RecruitmentOrders != nil {
		for _, order := range *plan.RecruitmentOrders {
			if _, ok := units[order.UnitType]; !ok {
				return fmt.Errorf("%w: unsupported recruitment unit %q", controller.ErrInvalidPlan, order.UnitType)
			}
		}
	}
	for _, unitType := range plan.RecruitmentPriorities {
		if _, ok := units[unitType]; !ok {
			return fmt.Errorf("%w: unsupported recruitment unit %q", controller.ErrInvalidPlan, unitType)
		}
	}
	return validateProductionSites(scenario, setup, plan)

}

// Static plan validation checks definitions and terrain. Execution rechecks
// dynamic range, spacing and occupancy after each earlier construction order.
func validateProductionSites(scenario Scenario, _ FactionSetup, plan controller.PlanProposal) error {
	legalBuildings := map[string]bool{}
	for _, b := range ConstructibleBuildings(scenario.Ruleset) {
		legalBuildings[b.ID] = true
	}
	for _, order := range constructionOrders(plan) {
		coordinate := order.Position
		if !legalBuildings[order.BuildingType] {
			return fmt.Errorf("%w: unsupported construction type %q", controller.ErrInvalidPlan, order.BuildingType)
		}
		if !scenario.Map.Buildable(Position{X: coordinate.X, Y: coordinate.Y}) {
			return fmt.Errorf("%w: construction site (%d,%d) is not legal", controller.ErrInvalidPlan, coordinate.X, coordinate.Y)
		}
	}
	return nil
}

// ProductionSchedule is the next point at which factions spend gold, for presentation.
type ProductionSchedule struct {
	NextTick uint64 `json:"nextTick"`
	Opening  bool   `json:"opening"`
}

// NextProduction derives the next spending point from a persisted snapshot using the
// same rules as OpeningDue and DecisionWindowDue: a pending opening purchase at tick
// 0, otherwise the next interval multiple. It reports false when no window remains
// before maxTicks.
func NextProduction(scenario Scenario, state State, maxTicks uint64) (ProductionSchedule, bool) {
	if scenario.SnapshotVersion >= DoctrineSnapshotVersion && !state.OpeningComplete {
		return ProductionSchedule{NextTick: 0, Opening: true}, true
	}
	interval := uint64(scenario.DecisionIntervalTicks)
	if interval == 0 {
		return ProductionSchedule{}, false
	}
	next := (state.Tick/interval + 1) * interval
	if next > maxTicks {
		return ProductionSchedule{}, false
	}
	return ProductionSchedule{NextTick: next}, true
}
