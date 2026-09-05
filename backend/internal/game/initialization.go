package game

import (
	"fmt"
	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
	"strings"
)

func NewMatch(id string, scenario Scenario, maxTicks uint64) (*Match, error) {
	if err := validateMatchScenario(id, scenario, maxTicks); err != nil {
		return nil, err
	}
	match := &Match{
		id: id, scenario: CloneScenario(scenario), maxTicks: maxTicks,
		state:    State{BattleID: id, Factions: make([]FactionState, 0, 2), Units: make([]Unit, 0), Buildings: make([]Building, 0, 6)},
		factions: make(map[string]*factionRuntime, 2),
	}
	occupied := make(map[Position]bool)
	for slot, setup := range scenario.Factions {
		if err := match.addFaction(slot, setup, occupied); err != nil {
			return nil, err
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
	if err := m.addStartingUnits(setup, runtime, availableSpawns(m.scenario.Map, castle.Position, occupied), occupied); err != nil {
		return err
	}
	m.factionState(setup.ID).UnitsStarted = runtime.unitSequence
	return nil
}

func newFactionRuntime(scenario Scenario, setup FactionSetup) (*factionRuntime, error) {
	if strings.TrimSpace(setup.ID) == "" || strings.TrimSpace(setup.Name) == "" {
		return nil, fmt.Errorf("%w: faction identity is invalid", ErrInvalidScenario)
	}
	if _, err := ValidateBlueprint(scenario.Ruleset, setup.Blueprint); err != nil {
		return nil, err
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
	for _, plan := range setup.ScriptedPlans {
		if err := controller.ValidatePlanProposal(plan, []string{ActionAttack, ActionDefend}); err != nil {
			return nil, err
		}
		if err := validateProductionPlan(scenario, setup, normalizedPlan(plan, setup)); err != nil {
			return nil, err
		}
	}
	attack, defense, economy := upgradeBonuses(scenario.Ruleset, setup.Blueprint)
	return &factionRuntime{setup: setup, activePlan: normalizedPlan(setup.FallbackPlan, setup), activeSource: "scripted_fallback", attackBonus: attack, defenseBonus: defense, economyBonus: economy}, nil
}

func (m *Match) addCastle(slot int, factionID string, occupied map[Position]bool) Building {
	rules := m.scenario.Ruleset.Buildings
	castle := Building{ID: factionID + ":castle", FactionID: factionID, Type: BuildingCastle, Position: m.scenario.Map.CastleSpawns[slot], Health: rules.CastleHealth, MaxHealth: rules.CastleHealth, Defense: rules.CastleDefense, Vision: rules.CastleVision, Income: rules.CastleIncome}
	m.state.Buildings = append(m.state.Buildings, castle)
	occupied[castle.Position] = true
	return castle
}

func availableSpawns(gameMap Map, castle Position, occupied map[Position]bool) []Position {
	spawns := make([]Position, 0, 6)
	for _, candidate := range Neighbors(castle) {
		if gameMap.InBounds(candidate) && gameMap.Traversable(candidate) && !occupied[candidate] {
			spawns = append(spawns, candidate)
		}
	}
	sortPositions(spawns)
	return spawns
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
			occupied[unit.Position] = true
			spawnIndex++
		}
	}
	return nil
}

func (f *factionRuntime) newUnit(definition UnitType, position Position, ready uint64) Unit {
	return Unit{ID: fmt.Sprintf("%s:%s:%03d", f.setup.ID, definition.ID, f.unitSequence), FactionID: f.setup.ID, Type: definition.ID, Position: position, Health: definition.Health, MaxHealth: definition.Health, Attack: definition.Attack + f.attackBonus, Defense: definition.Defense + f.defenseBonus, Speed: definition.Speed, Movement: definition.Movement, Range: definition.Range, Vision: definition.Vision, Cost: definition.Cost, ReadyAtTick: ready}
}
