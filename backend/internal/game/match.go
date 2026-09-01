package game

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/battle/simulation/controller"
)

type factionRuntime struct {
	setup        FactionSetup
	activePlan   controller.PlanProposal
	activeSource string
	planIndex    int
	unitSequence int
	attackBonus  int64
	defenseBonus int64
	economyBonus int64
	damageDealt  int64
	damageTaken  int64
	attackTicks  uint64
	defendTicks  uint64
}

type Match struct {
	id       string
	scenario Scenario
	maxTicks uint64
	state    State
	factions map[string]*factionRuntime
	sequence uint64
	started  bool
	finished bool
	result   *Result
}

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

func (m *Match) TickDuration() time.Duration { return m.scenario.TickDuration }
func (m *Match) State() State                { return CloneState(m.state) }
func (m *Match) Scenario() Scenario          { return CloneScenario(m.scenario) }
func (m *Match) Finished() bool              { return m.finished }

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
		ActivePlan: clonePlan(runtime.activePlan)}, nil
}

func (m *Match) FactionIDs() []string { return m.sortedFactionIDs() }

// ScriptedPlans resolves deterministic plans without exposing authoritative
// state mutation to the scripted controller path.
func (m *Match) ScriptedPlans() map[string]controller.PlanProposal {
	plans := make(map[string]controller.PlanProposal, len(m.factions))
	for _, factionID := range m.sortedFactionIDs() {
		plans[factionID] = m.normalizedFactionPlan(factionID, m.factions[factionID].scriptedPlan())
	}
	return plans
}

func (m *Match) Advance() (Transition, error) {
	var plans map[string]controller.PlanProposal
	if m.DecisionWindowDue() {
		plans = m.ScriptedPlans()
	}
	return m.AdvanceWithPlans(plans)
}

// AdvanceWithPlans applies plans only at production windows. Callers may omit
// a faction; its previous accepted plan (or first-window fallback) is reused.
func (m *Match) AdvanceWithPlans(plans map[string]controller.PlanProposal) (Transition, error) {
	if m.finished {
		return Transition{}, ErrMatchFinished
	}
	decisionWindow := m.DecisionWindowDue()
	if !decisionWindow && len(plans) > 0 {
		return Transition{}, fmt.Errorf("plans may only be applied at a decision window")
	}
	if decisionWindow {
		for factionID, runtime := range m.factions {
			runtime.activePlan = m.normalizedFactionPlan(factionID, runtime.activePlan)
		}
		for factionID, proposal := range plans {
			runtime := m.factions[factionID]
			if runtime == nil {
				return Transition{}, fmt.Errorf("unknown faction plan %q", factionID)
			}
			proposal = m.normalizedFactionPlan(factionID, proposal)
			if err := validateProductionPlan(m.scenario, runtime.setup, proposal); err != nil {
				return Transition{}, err
			}
			runtime.activePlan = clonePlan(proposal)
			runtime.activeSource = "accepted"
		}
	}
	m.state.Tick++
	m.clearUnitActions()
	events := m.startEvents()
	if decisionWindow {
		for _, factionID := range m.sortedFactionIDs() {
			runtime := m.factions[factionID]
			events = append(events, m.event("plan_applied", map[string]any{"factionId": factionID, "name": runtime.activePlan.Name, "source": runtime.activeSource, "targetTick": m.state.Tick}))
		}
	}
	events = append(events, m.runEconomy(decisionWindow)...)
	tactics, tacticEvents, err := m.selectTactics()
	if err != nil {
		return Transition{}, err
	}
	events = append(events, tacticEvents...)
	combatEvents, winner := m.runUnits(tactics)
	events = append(events, combatEvents...)
	events = append(events, m.event("tick_completed", map[string]any{"tick": m.state.Tick}))
	if winner != nil {
		m.finished = true
		m.result = m.buildResult(winner, "castle_destroyed")
		events = append(events, m.event("battle_completed", map[string]any{"winnerFactionId": *winner, "reason": "castle_destroyed", "ticksRun": m.state.Tick}))
	} else if m.state.Tick >= m.maxTicks {
		m.finished = true
		m.result = m.buildResult(nil, "tick_limit")
		events = append(events, m.event("battle_completed", map[string]any{"winnerFactionId": nil, "reason": "tick_limit", "ticksRun": m.state.Tick}))
	}
	m.sortState()
	transition := Transition{Tick: m.state.Tick, Events: events, State: CloneState(m.state), Finished: m.finished}
	if m.result != nil {
		result := CloneResult(*m.result)
		transition.Result = &result
	}
	return transition, nil
}

func (m *Match) startEvents() []Event {
	if m.started {
		return nil
	}
	m.started = true
	return []Event{m.event("battle_started", map[string]any{"battleId": m.id, "scenarioId": m.scenario.ID, "rulesVersion": m.scenario.RulesVersion, "rulesetId": m.scenario.RulesetID, "maximumTicks": m.maxTicks})}
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

func (m *Match) runEconomy(productionWindow bool) []Event {
	events := make([]Event, 0, 8)
	for slot, setup := range m.scenario.Factions {
		events = append(events, m.runFactionEconomy(slot, setup.ID, productionWindow)...)
	}
	return events
}

func (m *Match) runFactionEconomy(slot int, factionID string, productionWindow bool) []Event {
	castle := m.castle(factionID)
	if castle == nil || castle.Health <= 0 {
		return nil
	}
	state := m.factionState(factionID)
	income := m.incomeFor(factionID, castle.Income)
	state.Gold += income
	state.GoldEarned += income
	events := []Event{m.event("income_collected", map[string]any{"factionId": factionID, "amount": income, "balance": state.Gold})}
	if !productionWindow {
		return events
	}
	plan := m.factions[factionID].activePlan
	for _, kind := range plan.SpendOrder {
		switch kind {
		case "construction":
			events = append(events, m.runConstruction(slot, factionID, plan)...)
		case "recruitment":
			events = append(events, m.runRecruitment(factionID, plan)...)
		}
	}
	return events
}

func (m *Match) incomeFor(factionID string, castleIncome int64) int64 {
	income := castleIncome
	for index := range m.state.Buildings {
		building := &m.state.Buildings[index]
		if building.FactionID == factionID && building.Type == BuildingEconomy && building.Health > 0 {
			income += building.Income
		}
	}
	return income
}

func (m *Match) runConstruction(_ int, factionID string, plan controller.PlanProposal) []Event {
	events := make([]Event, 0, len(plan.ConstructionPriorities))
	for _, coordinate := range plan.ConstructionPriorities {
		site := Position{X: coordinate.X, Y: coordinate.Y}
		building, reason := m.buildEconomyBuildingAt(factionID, site, plan.ReserveGold)
		if reason != "" {
			events = append(events, m.event("production_skipped", map[string]any{"factionId": factionID, "kind": "construction", "position": site, "reason": reason}))
			continue
		}
		events = append(events, m.event("building_constructed", map[string]any{"factionId": factionID, "buildingId": building.ID, "buildingType": building.Type, "position": building.Position, "cost": building.Cost}))
	}
	return events
}

func (m *Match) buildEconomyBuildingAt(factionID string, site Position, reserve int64) (Building, string) {
	rules := m.scenario.Ruleset.Buildings
	state := m.factionState(factionID)
	if m.economyCount(factionID) >= rules.MaximumEconomyCount {
		return Building{}, "maximum_reached"
	}
	if m.buildingAt(site) != nil {
		return Building{}, "site_occupied"
	}
	if state.Gold-rules.EconomyCost < reserve {
		return Building{}, "reserve_required"
	}
	state.Gold -= rules.EconomyCost
	state.GoldSpent += rules.EconomyCost
	state.BuildingsBuilt++
	building := Building{ID: fmt.Sprintf("%s:economy:%03d", factionID, state.BuildingsBuilt), FactionID: factionID, Type: BuildingEconomy, Position: site, Health: rules.EconomyHealth, MaxHealth: rules.EconomyHealth, Defense: rules.EconomyDefense, Vision: rules.EconomyVision, Cost: rules.EconomyCost, Income: rules.EconomyIncome + m.factions[factionID].economyBonus}
	m.state.Buildings = append(m.state.Buildings, building)
	return building, ""
}

func (m *Match) runRecruitment(factionID string, plan controller.PlanProposal) []Event {
	runtime := m.factions[factionID]
	state := m.factionState(factionID)
	castle := m.castle(factionID)
	if castle == nil {
		return nil
	}
	definitions := UnitTypesByID(m.scenario.Ruleset)
	events := make([]Event, 0, 6)
	for {
		occupied := m.occupied()
		spawn := Position{X: -1, Y: -1}
		for _, candidate := range Neighbors(castle.Position) {
			if m.scenario.Map.InBounds(candidate) && m.scenario.Map.Traversable(candidate) && !occupied[candidate] {
				spawn = candidate
				break
			}
		}
		if spawn.X < 0 {
			events = append(events, m.event("production_skipped", map[string]any{"factionId": factionID, "kind": "recruitment", "reason": "no_spawn"}))
			break
		}
		selected := UnitType{}
		for _, unitType := range plan.RecruitmentPriorities {
			definition := definitions[unitType]
			if state.Gold-definition.Cost >= plan.ReserveGold {
				selected = definition
				break
			}
		}
		if selected.ID == "" {
			events = append(events, m.event("production_skipped", map[string]any{"factionId": factionID, "kind": "recruitment", "reason": "reserve_required"}))
			break
		}
		state.Gold -= selected.Cost
		state.GoldSpent += selected.Cost
		state.UnitsRecruited++
		runtime.unitSequence++
		unit := runtime.newUnit(selected, spawn, m.state.Tick+1)
		m.state.Units = append(m.state.Units, unit)
		events = append(events, m.event("unit_recruited", map[string]any{"factionId": factionID, "unitId": unit.ID, "unitType": unit.Type, "position": spawn, "readyAtTick": unit.ReadyAtTick, "cost": unit.Cost}))
	}
	return events
}

func (m *Match) runUnits(tactics map[string]string) ([]Event, *string) {
	defer m.removeDestroyedUnits()
	indices := m.unitTurnOrder()
	events := make([]Event, 0, len(indices)*2)
	for _, index := range indices {
		unit := &m.state.Units[index]
		if unit.Health <= 0 || unit.ReadyAtTick > m.state.Tick {
			continue
		}
		unitEvents, winner := m.runUnit(unit, tactics)
		events = append(events, unitEvents...)
		if winner != nil {
			return events, winner
		}
	}
	return events, nil
}

func (m *Match) unitTurnOrder() []int {
	indices := make([]int, 0, len(m.state.Units))
	for index := range m.state.Units {
		indices = append(indices, index)
	}
	sort.Slice(indices, func(i, j int) bool {
		left, right := m.state.Units[indices[i]], m.state.Units[indices[j]]
		if left.Speed != right.Speed {
			return left.Speed > right.Speed
		}
		return left.ID < right.ID
	})
	return indices
}

func (m *Match) runUnit(unit *Unit, tactics map[string]string) ([]Event, *string) {
	target := m.targetInRange(*unit)
	if target == nil {
		return m.moveUnit(unit), nil
	}
	return m.attackTarget(unit, target, tactics)
}

func (m *Match) moveUnit(unit *Unit) []Event {
	from := unit.Position
	m.moveTowardCastle(unit)
	if unit.Position == from {
		return nil
	}
	unit.MovedFrom = &from
	return []Event{m.event("unit_moved", map[string]any{"factionId": unit.FactionID, "unitId": unit.ID, "from": from, "to": unit.Position})}
}

func (m *Match) attackTarget(unit *Unit, target *attackTarget, tactics map[string]string) ([]Event, *string) {
	damage := m.damage(*unit, target.defense, tactics[unit.FactionID], tactics[target.factionID])
	targetPosition := target.position
	unit.AttackTargetID = target.id
	unit.AttackTargetPosition = &targetPosition
	actual := min(damage, target.health())
	target.applyDamage(actual)
	m.factions[unit.FactionID].damageDealt += actual
	m.factions[target.factionID].damageTaken += actual
	events := []Event{m.event("unit_attacked", map[string]any{"attackerId": unit.ID, "targetId": target.id, "targetType": target.kind, "damage": actual, "remainingHealth": target.health()})}
	if target.health() > 0 {
		return events, nil
	}
	if target.kind == BuildingCastle {
		events = append(events, m.event("castle_destroyed", map[string]any{"castleId": target.id, "factionId": target.factionID, "destroyedByFactionId": unit.FactionID}))
		winner := unit.FactionID
		return events, &winner
	}
	events = append(events, m.destroyTargetEvent(unit.FactionID, target))
	if lootEvent, ok := m.collectLoot(unit.FactionID, target); ok {
		events = append(events, lootEvent)
	}
	return events, nil
}

func (m *Match) destroyTargetEvent(attackerFactionID string, target *attackTarget) Event {
	if target.kind == "unit" {
		m.factionState(target.factionID).UnitsLost++
		return m.event("unit_destroyed", map[string]any{"unitId": target.id, "factionId": target.factionID})
	}
	m.factionState(attackerFactionID).BuildingsDestroyed++
	return m.event("building_destroyed", map[string]any{"buildingId": target.id, "factionId": target.factionID, "buildingType": target.kind})
}

func (m *Match) collectLoot(factionID string, target *attackTarget) (Event, bool) {
	loot := target.cost * m.scenario.Ruleset.Combat.LootPercent / 100
	if loot <= 0 {
		return Event{}, false
	}
	owner := m.factionState(factionID)
	owner.Gold += loot
	owner.GoldEarned += loot
	owner.GoldLooted += loot
	return m.event("loot_collected", map[string]any{"factionId": factionID, "sourceId": target.id, "amount": loot, "balance": owner.Gold}), true
}

func (m *Match) clearUnitActions() {
	for index := range m.state.Units {
		m.state.Units[index].MovedFrom = nil
		m.state.Units[index].AttackTargetID = ""
		m.state.Units[index].AttackTargetPosition = nil
	}
}

func (m *Match) removeDestroyedUnits() {
	survivors := make([]Unit, 0, len(m.state.Units))
	for _, unit := range m.state.Units {
		if unit.Health > 0 {
			survivors = append(survivors, unit)
		}
	}
	m.state.Units = survivors
}

type attackTarget struct {
	id, kind, factionID string
	position            Position
	defense, cost       int64
	unit                *Unit
	building            *Building
}

func (t *attackTarget) health() int64 {
	if t.unit != nil {
		return t.unit.Health
	}
	return t.building.Health
}
func (t *attackTarget) applyDamage(value int64) {
	if t.unit != nil {
		t.unit.Health -= value
	} else {
		t.building.Health -= value
	}
}

func (m *Match) targetInRange(attacker Unit) *attackTarget {
	candidates := make([]*attackTarget, 0)
	for index := range m.state.Units {
		unit := &m.state.Units[index]
		if unit.FactionID != attacker.FactionID && unit.Health > 0 && HexDistance(attacker.Position, unit.Position) <= attacker.Range {
			candidates = append(candidates, &attackTarget{id: unit.ID, kind: "unit", factionID: unit.FactionID, position: unit.Position, defense: unit.Defense, cost: unit.Cost, unit: unit})
		}
	}
	for index := range m.state.Buildings {
		building := &m.state.Buildings[index]
		if building.FactionID != attacker.FactionID && building.Health > 0 && HexDistance(attacker.Position, building.Position) <= attacker.Range {
			candidates = append(candidates, &attackTarget{id: building.ID, kind: building.Type, factionID: building.FactionID, position: building.Position, defense: building.Defense, cost: building.Cost, building: building})
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		return attackTargetLess(attacker, candidates[i], candidates[j])
	})
	return candidates[0]
}

func attackTargetLess(attacker Unit, left, right *attackTarget) bool {
	if attackTargetPriority(left.kind) != attackTargetPriority(right.kind) {
		return attackTargetPriority(left.kind) < attackTargetPriority(right.kind)
	}
	leftDistance, rightDistance := HexDistance(attacker.Position, left.position), HexDistance(attacker.Position, right.position)
	if leftDistance != rightDistance {
		return leftDistance < rightDistance
	}
	if left.health() != right.health() {
		return left.health() < right.health()
	}
	return left.id < right.id
}

func attackTargetPriority(kind string) int {
	if kind == "unit" {
		return 0
	}
	if kind == BuildingEconomy {
		return 1
	}
	return 2
}

func (m *Match) moveTowardCastle(unit *Unit) {
	castle := m.castle(m.opponentID(unit.FactionID))
	if castle == nil {
		return
	}
	goals := make(map[Position]bool)
	for y := 0; y < m.scenario.Map.Height; y++ {
		for x := 0; x < m.scenario.Map.Width; x++ {
			position := Position{X: x, Y: y}
			if m.scenario.Map.Traversable(position) && HexDistance(position, castle.Position) <= unit.Range {
				goals[position] = true
			}
		}
	}
	occupied := m.occupied()
	delete(occupied, unit.Position)
	path := shortestPath(m.scenario.Map, unit.Position, goals, occupied)
	steps := min(unit.Movement, len(path))
	if steps > 0 {
		unit.Position = path[steps-1]
	}
}

func (m *Match) damage(attacker Unit, defense int64, attackerTactic, targetTactic string) int64 {
	value := attacker.Attack - defense
	if value < 0 {
		value = 0
	}
	if attackerTactic == ActionDefend {
		value = value * m.scenario.Ruleset.Combat.DefendAttackPercent / 100
	}
	if targetTactic == ActionDefend {
		value = value * m.scenario.Ruleset.Combat.DefendDamageTakenPercent / 100
	}
	return max(value, m.scenario.Ruleset.Combat.MinimumDamage)
}

func (m *Match) buildResult(winner *string, reason string) *Result {
	result := &Result{WinnerFactionID: winner, Reason: reason, TicksRun: m.state.Tick, Factions: make([]FactionStats, 0, 2)}
	for _, id := range m.sortedFactionIDs() {
		state, runtime := m.factionState(id), m.factions[id]
		remaining := 0
		for _, unit := range m.state.Units {
			if unit.FactionID == id && unit.Health > 0 {
				remaining++
			}
		}
		castleHealth := int64(0)
		if castle := m.castle(id); castle != nil {
			castleHealth = max(int64(0), castle.Health)
		}
		result.Factions = append(result.Factions, FactionStats{FactionID: id, CastleHealth: castleHealth, UnitsStarted: state.UnitsStarted, UnitsRecruited: state.UnitsRecruited, UnitsLost: state.UnitsLost, UnitsRemaining: remaining, DamageDealt: runtime.damageDealt, DamageTaken: runtime.damageTaken, AttackTicks: runtime.attackTicks, DefendTicks: runtime.defendTicks, GoldEarned: state.GoldEarned, GoldSpent: state.GoldSpent, GoldLooted: state.GoldLooted, BuildingsConstructed: state.BuildingsBuilt, BuildingsDestroyed: state.BuildingsDestroyed})
	}
	return result
}

func (m *Match) View(factionID string) (View, error) {
	return ProjectView(m.scenario.Map, m.state, factionID)
}

// ProjectView applies v1 fog-of-war to a persisted state snapshot. Terrain is
// always returned; hostile non-castle entities are omitted outside vision.
func ProjectView(gameMap Map, source State, factionID string) (View, error) {
	if factionID == "" {
		positions := allPositions(gameMap)
		return View{Map: CloneMap(gameMap), State: CloneState(source), VisiblePositions: positions}, nil
	}
	if !factionParticipates(source, factionID) {
		return View{}, fmt.Errorf("faction %q does not participate", factionID)
	}
	visible := visiblePositionsForFaction(gameMap, source, factionID)
	state := visibleState(source, factionID, visible)
	return View{Map: CloneMap(gameMap), State: state, VisiblePositions: sortedPositions(visible)}, nil
}

func factionParticipates(state State, factionID string) bool {
	for _, faction := range state.Factions {
		if faction.FactionID == factionID {
			return true
		}
	}
	return false
}

func visiblePositionsForFaction(gameMap Map, state State, factionID string) map[Position]bool {
	visible := make(map[Position]bool)
	for _, unit := range state.Units {
		if unit.FactionID == factionID && unit.Health > 0 {
			addVision(visible, gameMap, unit.Position, unit.Vision)
		}
	}
	for _, building := range state.Buildings {
		if building.FactionID == factionID && building.Health > 0 {
			addVision(visible, gameMap, building.Position, building.Vision)
		}
	}
	return visible
}

func visibleState(source State, factionID string, visible map[Position]bool) State {
	state := CloneState(source)
	state.Units = state.Units[:0]
	for _, unit := range source.Units {
		if unit.FactionID == factionID || visible[unit.Position] {
			state.Units = append(state.Units, unit)
		}
	}
	state.Buildings = state.Buildings[:0]
	for _, building := range source.Buildings {
		if building.FactionID == factionID || building.Type == BuildingCastle || visible[building.Position] {
			state.Buildings = append(state.Buildings, building)
		}
	}
	return state
}

func sortedPositions(values map[Position]bool) []Position {
	positions := make([]Position, 0, len(values))
	for position := range values {
		positions = append(positions, position)
	}
	sortPositions(positions)
	return positions
}

func (m *Match) event(kind string, data map[string]any) Event {
	m.sequence++
	return Event{Sequence: m.sequence, Tick: m.state.Tick, Type: kind, Data: data}
}
func (m *Match) sortedFactionIDs() []string {
	ids := make([]string, 0, len(m.factions))
	for id := range m.factions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
func (m *Match) opponentID(id string) string {
	for _, setup := range m.scenario.Factions {
		if setup.ID != id {
			return setup.ID
		}
	}
	return ""
}
func (m *Match) factionState(id string) *FactionState {
	for index := range m.state.Factions {
		if m.state.Factions[index].FactionID == id {
			return &m.state.Factions[index]
		}
	}
	return nil
}
func (m *Match) castle(id string) *Building {
	for index := range m.state.Buildings {
		if m.state.Buildings[index].FactionID == id && m.state.Buildings[index].Type == BuildingCastle {
			return &m.state.Buildings[index]
		}
	}
	return nil
}
func (m *Match) buildingAt(position Position) *Building {
	for index := range m.state.Buildings {
		if m.state.Buildings[index].Health > 0 && m.state.Buildings[index].Position == position {
			return &m.state.Buildings[index]
		}
	}
	return nil
}
func (m *Match) economyCount(id string) int {
	count := 0
	for _, building := range m.state.Buildings {
		if building.FactionID == id && building.Type == BuildingEconomy && building.Health > 0 {
			count++
		}
	}
	return count
}
func (m *Match) occupied() map[Position]bool {
	values := make(map[Position]bool)
	for _, unit := range m.state.Units {
		if unit.Health > 0 {
			values[unit.Position] = true
		}
	}
	for _, building := range m.state.Buildings {
		if building.Health > 0 {
			values[building.Position] = true
		}
	}
	return values
}
func (m *Match) projectedPower(id string) int64 {
	var value int64
	for _, unit := range m.state.Units {
		if unit.FactionID == id && unit.Health > 0 {
			value += unit.Attack
		}
	}
	return value
}
func (m *Match) sortState() {
	sort.Slice(m.state.Factions, func(i, j int) bool { return m.state.Factions[i].FactionID < m.state.Factions[j].FactionID })
	sort.Slice(m.state.Units, func(i, j int) bool { return m.state.Units[i].ID < m.state.Units[j].ID })
	sort.Slice(m.state.Buildings, func(i, j int) bool { return m.state.Buildings[i].ID < m.state.Buildings[j].ID })
}

func (f *factionRuntime) newUnit(definition UnitType, position Position, ready uint64) Unit {
	return Unit{ID: fmt.Sprintf("%s:%s:%03d", f.setup.ID, definition.ID, f.unitSequence), FactionID: f.setup.ID, Type: definition.ID, Position: position, Health: definition.Health, MaxHealth: definition.Health, Attack: definition.Attack + f.attackBonus, Defense: definition.Defense + f.defenseBonus, Speed: definition.Speed, Movement: definition.Movement, Range: definition.Range, Vision: definition.Vision, Cost: definition.Cost, ReadyAtTick: ready}
}
func (f *factionRuntime) scriptedPlan() controller.PlanProposal {
	index := f.planIndex
	if index >= len(f.setup.ScriptedPlans) {
		index = len(f.setup.ScriptedPlans) - 1
	}
	f.planIndex++
	return clonePlan(f.setup.ScriptedPlans[index])
}

func normalizedPlan(value controller.PlanProposal, setup FactionSetup) controller.PlanProposal {
	result := clonePlan(value)
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

func addVision(visible map[Position]bool, m Map, origin Position, radius int) {
	for y := 0; y < m.Height; y++ {
		for x := 0; x < m.Width; x++ {
			position := Position{X: x, Y: y}
			if HexDistance(origin, position) <= radius {
				visible[position] = true
			}
		}
	}
}
func allPositions(m Map) []Position {
	result := make([]Position, 0, m.Width*m.Height)
	for y := 0; y < m.Height; y++ {
		for x := 0; x < m.Width; x++ {
			result = append(result, Position{X: x, Y: y})
		}
	}
	return result
}

func CloneState(value State) State {
	value.Factions = append([]FactionState(nil), value.Factions...)
	value.Units = append([]Unit(nil), value.Units...)
	for index := range value.Units {
		value.Units[index].MovedFrom = clonePosition(value.Units[index].MovedFrom)
		value.Units[index].AttackTargetPosition = clonePosition(value.Units[index].AttackTargetPosition)
	}
	value.Buildings = append([]Building(nil), value.Buildings...)
	return value
}

func clonePosition(value *Position) *Position {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
func CloneMap(value Map) Map {
	value.Rows = append([]string(nil), value.Rows...)
	for i := range value.EconomySites {
		value.EconomySites[i] = append([]Position(nil), value.EconomySites[i]...)
	}
	return value
}
func CloneResult(value Result) Result {
	value.Factions = append([]FactionStats(nil), value.Factions...)
	if value.WinnerFactionID != nil {
		winner := *value.WinnerFactionID
		value.WinnerFactionID = &winner
	}
	return value
}
func CloneScenario(value Scenario) Scenario {
	value.Map = CloneMap(value.Map)
	value.Ruleset.Units = append([]UnitType(nil), value.Ruleset.Units...)
	value.Ruleset.Upgrades = append([]Upgrade(nil), value.Ruleset.Upgrades...)
	value.Factions = append([]FactionSetup(nil), value.Factions...)
	for i := range value.Factions {
		value.Factions[i].Blueprint = CloneBlueprint(value.Factions[i].Blueprint)
		value.Factions[i].FallbackPlan = clonePlan(value.Factions[i].FallbackPlan)
		value.Factions[i].ScriptedPlans = clonePlans(value.Factions[i].ScriptedPlans)
	}
	return value
}
func clonePlan(value controller.PlanProposal) controller.PlanProposal {
	result := controller.PlanProposal{Name: value.Name, Rationale: value.Rationale, ActionWeights: make(map[string]int64, len(value.ActionWeights)), TargetBonuses: make(map[string]int64, len(value.TargetBonuses)), ReserveGold: value.ReserveGold,
		SpendOrder: append([]string(nil), value.SpendOrder...), ConstructionPriorities: append([]controller.Coordinate(nil), value.ConstructionPriorities...), RecruitmentPriorities: append([]string(nil), value.RecruitmentPriorities...)}
	for k, v := range value.ActionWeights {
		result.ActionWeights[k] = v
	}
	for k, v := range value.TargetBonuses {
		result.TargetBonuses[k] = v
	}
	return result
}
func clonePlans(values []controller.PlanProposal) []controller.PlanProposal {
	result := make([]controller.PlanProposal, len(values))
	for i := range values {
		result[i] = clonePlan(values[i])
	}
	return result
}
