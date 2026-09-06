package game

import (
	"fmt"
	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
	"sort"
	"time"
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

func (m *Match) TickDuration() time.Duration { return m.scenario.TickDuration }

func (m *Match) State() State { return CloneState(m.state) }

func (m *Match) Scenario() Scenario { return CloneScenario(m.scenario) }

func (m *Match) Finished() bool { return m.finished }

func (m *Match) FactionIDs() []string { return m.sortedFactionIDs() }

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
	if m.OpeningDue() {
		return m.advanceOpening(plans)
	}
	decisionWindow := m.DecisionWindowDue()
	if !decisionWindow && len(plans) > 0 {
		return Transition{}, fmt.Errorf("plans may only be applied at a decision window")
	}
	if decisionWindow {
		if err := m.applyPlans(plans); err != nil {
			return Transition{}, err
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
	events = append(events, m.completionEvents(winner)...)
	m.sortState()
	return m.transition(events), nil
}

func (m *Match) completionEvents(winner *string) []Event {
	if winner != nil {
		m.finished = true
		m.result = m.buildResult(winner, "castle_destroyed")
		return []Event{m.event("battle_completed", map[string]any{"winnerFactionId": *winner, "reason": "castle_destroyed", "ticksRun": m.state.Tick})}
	}
	if m.state.Tick < m.maxTicks {
		return nil
	}
	m.finished = true
	m.result = m.buildResult(nil, "tick_limit")
	return []Event{m.event("battle_completed", map[string]any{"winnerFactionId": nil, "reason": "tick_limit", "ticksRun": m.state.Tick})}
}

func (m *Match) transition(events []Event) Transition {
	transition := Transition{Tick: m.state.Tick, Events: events, State: CloneState(m.state), Finished: m.finished}
	if m.result != nil {
		result := CloneResult(*m.result)
		transition.Result = &result
	}
	return transition
}

func (m *Match) startEvents() []Event {
	if m.started {
		return nil
	}
	m.started = true
	return []Event{m.event("battle_started", map[string]any{"battleId": m.id, "scenarioId": m.scenario.ID, "rulesVersion": m.scenario.RulesVersion, "rulesetId": m.scenario.RulesetID, "maximumTicks": m.maxTicks})}
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

func (m *Match) sortState() {
	sort.Slice(m.state.Factions, func(i, j int) bool { return m.state.Factions[i].FactionID < m.state.Factions[j].FactionID })
	sort.Slice(m.state.Units, func(i, j int) bool { return m.state.Units[i].ID < m.state.Units[j].ID })
	sort.Slice(m.state.Buildings, func(i, j int) bool { return m.state.Buildings[i].ID < m.state.Buildings[j].ID })
}
