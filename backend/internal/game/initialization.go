// Validates a resolved scenario and creates the match's initial faction runtimes, castles,
// and starting units.

package game

import (
	"fmt"
	"strings"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/game/controller"
)

func NewMatch(id string, scenario Scenario, maxTicks uint64) (*Match, error) {
	if err := validateMatchScenario(id, scenario, maxTicks); err != nil {
		return nil, err
	}
	match := &Match{
		id: id, scenario: CloneScenario(scenario), maxTicks: maxTicks,
		state:    State{BattleID: id, Factions: make([]FactionState, 0, 2), Units: make([]Unit, 0), Buildings: make([]Building, 0, 6), GoldMines: initialGoldMines(scenario.Map, scenario.Ruleset.Buildings)},
		factions: make(map[string]*factionRuntime, 2),
	}
	occupied := make(map[Position]bool)
	for slot, setup := range scenario.Factions {
		if err := match.addFaction(slot, setup, occupied); err != nil {
			return nil, err
		}
	}
	if err := match.addStartingBarracks(); err != nil {
		return nil, err
	}
	if scenario.Ruleset.ContinuousProduction {
		for id, runtime := range match.factions {
			runtime.activePlan = match.scriptedProductionPlan(id, runtime.activePlan)
		}
	}
	match.sortState()
	return match, nil
}

func validateMatchScenario(id string, scenario Scenario, maxTicks uint64) error {
	if strings.TrimSpace(id) == "" || maxTicks == 0 || len(scenario.Factions) != 2 || scenario.Factions[0].ID == scenario.Factions[1].ID {
		return fmt.Errorf("%w: battle ID, ticks, and two unique factions are required", ErrInvalidScenario)
	}
	if scenario.RulesetID != scenario.Ruleset.ID || strings.TrimSpace(scenario.ID) == "" || strings.TrimSpace(scenario.RulesVersion) == "" || scenario.TickDuration < 0 {
		return fmt.Errorf("%w: scenario metadata is invalid", ErrInvalidScenario)
	}
	if err := ValidateMap(scenario.Map); err != nil {
		return err
	}
	if err := ValidateRuleset(scenario.Ruleset); err != nil {
		return err
	}
	if scenario.DecisionIntervalTicks == 0 {
		return fmt.Errorf("%w: decision timing is invalid", ErrInvalidScenario)
	}
	return nil
}

func (m *Match) addFaction(slot int, setup FactionSetup, occupied map[Position]bool) error {
	runtime, err := newFactionRuntime(m.scenario, setup)
	if err != nil {
		return err
	}
	m.factions[setup.ID] = runtime
	m.state.Factions = append(m.state.Factions, FactionState{FactionID: setup.ID, Gold: m.scenario.Ruleset.StartingGold})
	castle := m.addCastle(slot, setup.ID, occupied)
	if m.scenario.SnapshotVersion >= DoctrineSnapshotVersion {
		return nil
	}
	if err := m.addStartingUnits(setup, runtime, freeNeighbors(m.scenario.Map, castle.Position, occupied), occupied); err != nil {
		return err
	}
	m.factionState(setup.ID).UnitsStarted = runtime.unitSequence
	return nil
}

func newFactionRuntime(scenario Scenario, setup FactionSetup) (*factionRuntime, error) {
	if strings.TrimSpace(setup.ID) == "" || strings.TrimSpace(setup.Name) == "" {
		return nil, fmt.Errorf("%w: faction identity is invalid", ErrInvalidScenario)
	}
	if scenario.SnapshotVersion < DoctrineSnapshotVersion {
		if _, err := ValidateBlueprint(scenario.Ruleset, setup.Blueprint); err != nil {
			return nil, err
		}
	} else {
		setup.Blueprint = Blueprint{}
	}
	if len(setup.ScriptedPlans) == 0 || strings.TrimSpace(setup.FallbackPlan.Name) == "" {
		return nil, fmt.Errorf("%w: faction %q requires controller plans", ErrInvalidScenario, setup.ID)
	}
	if setup.Controller.Type == "" {
		setup.Controller.Type = "scripted"
	}
	if setup.Controller.Type != "scripted" && setup.Controller.Type != "model" {
		return nil, fmt.Errorf("%w: faction %q has invalid controller type", ErrInvalidScenario, setup.ID)
	}
	if setup.Controller.Type == "model" && strings.TrimSpace(setup.Controller.ProfileID) == "" {
		return nil, fmt.Errorf("%w: faction %q model controller requires a profile", ErrInvalidScenario, setup.ID)
	}
	if err := validateFactionPlans(scenario, setup); err != nil {
		return nil, err
	}
	attack, defense, economy := upgradeBonuses(scenario.Ruleset, setup.Blueprint)
	runtime := &factionRuntime{setup: setup, activePlan: normalizedPlan(setup.FallbackPlan, setup), activeSource: "scripted_fallback", attackBonus: attack, defenseBonus: defense, economyBonus: economy}
	runtime.scopedUpgrades = blueprintScopedUpgrades(scenario.Ruleset, setup.Blueprint)
	return runtime, nil
}

func blueprintScopedUpgrades(ruleset Ruleset, blueprint Blueprint) []Upgrade {
	var upgrades []Upgrade
	definitions := UpgradesByID(ruleset)
	for _, id := range blueprint.UpgradeIDs {
		u := definitions[id]
		if u.TargetUnit != "" || u.TargetClass != "" {
			upgrades = append(upgrades, u)
		}
	}
	return upgrades
}

func (m *Match) addCastle(slot int, factionID string, occupied map[Position]bool) Building {
	rules := m.scenario.Ruleset.Buildings
	castle := Building{Trains: append([]string(nil), rules.CastleTrains...), Attack: rules.CastleAttack, Range: rules.CastleRange, BuildRange: effectiveBuildRange(rules.CastleBuildRange), ID: factionID + ":castle", FactionID: factionID, Type: BuildingCastle, Position: m.scenario.Map.CastleSpawns[slot], Health: rules.CastleHealth, MaxHealth: rules.CastleHealth, Defense: rules.CastleDefense, Vision: rules.CastleVision, Income: rules.CastleIncome}
	m.initializeProduction(&castle)
	m.state.Buildings = append(m.state.Buildings, castle)
	occupied[castle.Position] = true
	return castle
}

func (m *Match) addStartingUnits(setup FactionSetup, runtime *factionRuntime, spawns []Position, occupied map[Position]bool) error {
	spawnIndex := 0
	definitions := UnitTypesByID(m.scenario.Ruleset)
	for _, selection := range normalizedSelections(setup.Blueprint) {
		for count := 0; count < selection.Count; count++ {
			if spawnIndex >= len(spawns) {
				return fmt.Errorf("%w: faction %q has no free spawn", ErrInvalidScenario, setup.ID)
			}
			runtime.unitSequence++
			unit := runtime.newUnit(definitions[selection.Type], spawns[spawnIndex], 0)
			m.state.Units = append(m.state.Units, unit)
			runtime.stats.unit(unit.Type).Started++
			occupied[unit.Position] = true
			spawnIndex++
		}
	}
	return nil
}

func (f *factionRuntime) newUnit(definition UnitType, position Position, ready uint64) Unit {
	attack := definition.Attack + f.attackBonus
	if definition.HealAmount > 0 || definition.RepairAmount > 0 {
		attack = 0
	}
	unit := Unit{Scout: definition.Scout, PreferredTargetClass: definition.PreferredTargetClass, ArmorPiercing: definition.ArmorPiercing, RepairAmount: definition.RepairAmount, RepairCost: definition.RepairCost, RepairCharges: definition.RepairCharges, HealRange: definition.HealRange, AttackMode: definition.AttackMode, Class: definition.Class, MinimumRange: definition.MinimumRange, BuildingPriority: definition.BuildingPriority, BuildingDamageBonus: definition.BuildingDamageBonus, BonusAgainstClass: definition.BonusAgainstClass, ClassDamageBonus: definition.ClassDamageBonus, HealAmount: definition.HealAmount, ID: fmt.Sprintf("%s:%s:%03d", f.setup.ID, definition.ID, f.unitSequence), FactionID: f.setup.ID, Type: definition.ID, Position: position, Health: definition.Health, MaxHealth: definition.Health, Attack: attack, Defense: definition.Defense + f.defenseBonus, Speed: definition.Speed, Movement: definition.Movement, Range: definition.Range, Vision: definition.Vision, Cost: definition.Cost, ReadyAtTick: ready}
	for _, u := range f.scopedUpgrades {
		applyUnitUpgrade(&unit, u)
	}
	return unit
}

func validateFactionPlans(scenario Scenario, setup FactionSetup) error {
	for _, plan := range setup.ScriptedPlans {
		if err := controller.ValidatePlanProposal(plan, []string{ActionAttack, ActionDefend}); err != nil {
			return err
		}
		if err := validateProductionPlan(scenario, setup, normalizedPlan(plan, setup)); err != nil {
			return err
		}
	}
	return nil
}
