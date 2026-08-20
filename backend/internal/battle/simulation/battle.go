package simulation

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/battle/simulation/controller"
)

const (
	maxUtility  = int64(1_000_000)
	maxUnitStat = int64(1_000_000_000)
)

type factionRuntime struct {
	setup      FactionSetup
	controller controller.Controller
	planIndex  int
	stats      FactionStats
}

type Battle struct {
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

func NewBattle(id string, scenario Scenario, maxTicks uint64) (*Battle, error) {
	if err := validateBattle(id, scenario, maxTicks); err != nil {
		return nil, err
	}

	battle := &Battle{
		id:       id,
		scenario: cloneScenario(scenario),
		maxTicks: maxTicks,
		state:    State{BattleID: id, Units: make([]Unit, 0)},
		factions: make(map[string]*factionRuntime, len(scenario.Factions)),
	}
	for _, faction := range scenario.Factions {
		for index, proposal := range faction.ScriptedPlans {
			if err := controller.ValidatePlanProposal(proposal, []string{ActionAttack, ActionDefend}); err != nil {
				return nil, fmt.Errorf("%w: faction %q scripted plan %d: %v", ErrInvalidBattle, faction.ID, index, err)
			}
		}
		controller, err := controller.NewUtilityController(controller.Config{
			DecisionIntervalTicks: scenario.DecisionIntervalTicks,
			DecisionLeadTicks:     scenario.DecisionLeadTicks,
			FirstDecisionTick:     1,
			DefaultActionWeight:   1000,
			ActionKinds:           []string{ActionAttack, ActionDefend},
			FallbackPlan:          faction.FallbackPlan,
			Traits:                faction.Traits,
		})
		if err != nil {
			return nil, fmt.Errorf("%w: faction %q controller: %v", ErrInvalidBattle, faction.ID, err)
		}
		runtime := &factionRuntime{
			setup:      faction,
			controller: controller,
			stats: FactionStats{
				FactionID:    faction.ID,
				UnitsStarted: len(faction.Units),
			},
		}
		battle.factions[faction.ID] = runtime
		for _, unit := range faction.Units {
			battle.state.Units = append(battle.state.Units, Unit{
				ID:        faction.ID + ":" + unit.ID,
				FactionID: faction.ID,
				Health:    unit.Health,
				Attack:    unit.Attack,
				Defense:   unit.Defense,
				Speed:     unit.Speed,
			})
		}
	}
	return battle, nil
}

func (b *Battle) TickDuration() time.Duration { return b.scenario.TickDuration }

func (b *Battle) Finished() bool { return b.finished }

func (b *Battle) State() State {
	state := b.state
	state.Units = append([]Unit(nil), b.state.Units...)
	return state
}

func (b *Battle) Result() (Result, error) {
	if !b.finished || b.result == nil {
		return Result{}, ErrBattleNotFinished
	}
	return cloneResult(*b.result), nil
}

func (b *Battle) Advance() (Transition, error) {
	if b.finished {
		return Transition{}, ErrBattleFinished
	}

	b.state.Tick++
	events := make([]Event, 0)
	if !b.started {
		b.started = true
		events = append(events, b.event("battle_started", map[string]any{
			"battleId":     b.id,
			"scenarioId":   b.scenario.ID,
			"rulesVersion": b.scenario.RulesVersion,
			"maximumTicks": b.maxTicks,
		}))
	}

	tactics := make(map[string]string, len(b.factions))
	for _, faction := range b.sortedFactions() {
		observation := b.observation(faction.setup.ID)
		decision, err := faction.controller.Tick(observation)
		if err != nil {
			return Transition{}, fmt.Errorf("controller tick for faction %q: %w", faction.setup.ID, err)
		}
		if request := decision.DecisionRequest; request != nil {
			events = append(events, b.event("decision_requested", map[string]any{
				"factionId":   faction.setup.ID,
				"requestId":   request.ID,
				"applyAtTick": uint64(request.ApplyAtTick),
			}))
			proposal := faction.scriptedPlan()
			plan, err := faction.controller.SubmitPlan(request.ID, proposal)
			if err != nil {
				return Transition{}, fmt.Errorf("submit scripted plan for faction %q: %w", faction.setup.ID, err)
			}
			events = append(events, b.event("plan_accepted", map[string]any{
				"factionId":      faction.setup.ID,
				"planId":         plan.ID,
				"name":           plan.Name,
				"validFromTick":  uint64(plan.ValidFromTick),
				"validUntilTick": uint64(plan.ValidUntilTick),
			}))
		}
		tactic := ActionAttack
		if decision.Selection != nil {
			tactic = decision.Selection.CandidateID
		}
		tactics[faction.setup.ID] = tactic
		if tactic == ActionDefend {
			faction.stats.DefendTicks++
		} else {
			faction.stats.AttackTicks++
		}
		events = append(events, b.event("tactic_selected", map[string]any{
			"factionId": faction.setup.ID,
			"tactic":    tactic,
			"planId":    decision.PlanID,
			"fallback":  decision.UsedFallback,
		}))
	}

	initiative := make([]*Unit, 0, len(b.state.Units))
	for index := range b.state.Units {
		initiative = append(initiative, &b.state.Units[index])
	}
	sort.Slice(initiative, func(i, j int) bool {
		if initiative[i].Speed != initiative[j].Speed {
			return initiative[i].Speed > initiative[j].Speed
		}
		return initiative[i].ID < initiative[j].ID
	})
	for _, attacker := range initiative {
		if attacker.Health <= 0 {
			continue
		}
		target := b.targetFor(attacker.FactionID)
		if target == nil {
			break
		}
		damage := b.damage(*attacker, *target, tactics[attacker.FactionID], tactics[target.FactionID])
		actualDamage := min(damage, target.Health)
		target.Health -= actualDamage
		b.factions[attacker.FactionID].stats.DamageDealt += actualDamage
		b.factions[target.FactionID].stats.DamageTaken += actualDamage
		events = append(events, b.event("unit_attacked", map[string]any{
			"attackerId":      attacker.ID,
			"targetId":        target.ID,
			"damage":          actualDamage,
			"remainingHealth": target.Health,
		}))
		if target.Health == 0 {
			events = append(events, b.event("unit_destroyed", map[string]any{
				"unitId":    target.ID,
				"factionId": target.FactionID,
			}))
		}
	}

	events = append(events, b.event("tick_completed", map[string]any{"tick": b.state.Tick}))
	winner := b.winner()
	if winner != nil || b.state.Tick >= b.maxTicks {
		b.finished = true
		reason := "tick_limit"
		if winner != nil {
			reason = "elimination"
		}
		result := b.buildResult(winner, reason)
		b.result = &result
		events = append(events, b.event("battle_completed", map[string]any{
			"winnerFactionId": winner,
			"reason":          reason,
			"ticksRun":        b.state.Tick,
		}))
	}

	transition := Transition{Tick: b.state.Tick, Events: events, Finished: b.finished}
	if b.result != nil {
		result := cloneResult(*b.result)
		transition.Result = &result
	}
	return transition, nil
}

func (f *factionRuntime) scriptedPlan() controller.PlanProposal {
	index := f.planIndex
	if index >= len(f.setup.ScriptedPlans) {
		index = len(f.setup.ScriptedPlans) - 1
	}
	f.planIndex++
	return f.setup.ScriptedPlans[index]
}

func (b *Battle) observation(factionID string) controller.Observation {
	opponentID := b.opponentID(factionID)
	return controller.Observation{
		BattlefieldID: b.id,
		FactionID:     factionID,
		Tick:          controller.Tick(b.state.Tick),
		Version:       b.state.Tick,
		Candidates: []controller.Candidate{
			{ID: ActionAttack, Kind: ActionAttack, TargetID: opponentID, BaseUtility: b.projectedDamage(factionID, opponentID)},
			{ID: ActionDefend, Kind: ActionDefend, TargetID: factionID, BaseUtility: b.projectedDamage(opponentID, factionID)},
		},
	}
}

func (b *Battle) projectedDamage(attackerFactionID, targetFactionID string) int64 {
	var total int64
	for _, attacker := range b.state.Units {
		if attacker.FactionID != attackerFactionID || attacker.Health <= 0 {
			continue
		}
		target := b.targetInFaction(targetFactionID)
		if target == nil {
			continue
		}
		damage := attacker.Attack - target.Defense
		if damage < b.scenario.Rules.MinimumDamage {
			damage = b.scenario.Rules.MinimumDamage
		}
		total += damage
		if total >= maxUtility {
			return maxUtility
		}
	}
	return total
}

func (b *Battle) targetFor(attackerFactionID string) *Unit {
	return b.targetInFaction(b.opponentID(attackerFactionID))
}

func (b *Battle) targetInFaction(factionID string) *Unit {
	var selected *Unit
	for index := range b.state.Units {
		unit := &b.state.Units[index]
		if unit.FactionID != factionID || unit.Health <= 0 {
			continue
		}
		if selected == nil || unit.Health < selected.Health ||
			(unit.Health == selected.Health && unit.ID < selected.ID) {
			selected = unit
		}
	}
	return selected
}

func (b *Battle) damage(attacker, target Unit, attackerTactic, targetTactic string) int64 {
	damage := attacker.Attack - target.Defense
	if damage < 0 {
		damage = 0
	}
	if attackerTactic == ActionDefend {
		damage = damage * b.scenario.Rules.DefendAttackPercent / 100
	}
	if targetTactic == ActionDefend {
		damage = damage * b.scenario.Rules.DefendDamageTakenPercent / 100
	}
	if damage < b.scenario.Rules.MinimumDamage {
		damage = b.scenario.Rules.MinimumDamage
	}
	return damage
}

func (b *Battle) opponentID(factionID string) string {
	for _, faction := range b.scenario.Factions {
		if faction.ID != factionID {
			return faction.ID
		}
	}
	return ""
}

func (b *Battle) winner() *string {
	alive := make(map[string]bool, len(b.factions))
	for _, unit := range b.state.Units {
		if unit.Health > 0 {
			alive[unit.FactionID] = true
		}
	}
	if len(alive) != 1 {
		return nil
	}
	for factionID := range alive {
		winner := factionID
		return &winner
	}
	return nil
}

func (b *Battle) buildResult(winner *string, reason string) Result {
	stats := make([]FactionStats, 0, len(b.factions))
	for _, faction := range b.sortedFactions() {
		faction.stats.UnitsRemaining = 0
		for _, unit := range b.state.Units {
			if unit.FactionID == faction.setup.ID && unit.Health > 0 {
				faction.stats.UnitsRemaining++
			}
		}
		stats = append(stats, faction.stats)
	}
	return Result{WinnerFactionID: cloneStringPointer(winner), Reason: reason, TicksRun: b.state.Tick, Factions: stats}
}

func (b *Battle) sortedFactions() []*factionRuntime {
	factions := make([]*factionRuntime, 0, len(b.factions))
	for _, faction := range b.factions {
		factions = append(factions, faction)
	}
	sort.Slice(factions, func(i, j int) bool { return factions[i].setup.ID < factions[j].setup.ID })
	return factions
}

func (b *Battle) event(eventType string, data map[string]any) Event {
	b.sequence++
	return Event{Sequence: b.sequence, Tick: b.state.Tick, Type: eventType, Data: data}
}

func validateBattle(id string, scenario Scenario, maxTicks uint64) error {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(scenario.ID) == "" || strings.TrimSpace(scenario.RulesVersion) == "" {
		return fmt.Errorf("%w: battle, scenario, and rules version IDs are required", ErrInvalidBattle)
	}
	if maxTicks == 0 {
		return fmt.Errorf("%w: maximum ticks must be positive", ErrInvalidBattle)
	}
	if scenario.TickDuration < 0 || scenario.TickDuration > 10*time.Second {
		return fmt.Errorf("%w: tick duration must be between 0 and 10s", ErrInvalidBattle)
	}
	if scenario.DecisionIntervalTicks == 0 || scenario.DecisionLeadTicks == 0 || scenario.DecisionLeadTicks >= scenario.DecisionIntervalTicks {
		return fmt.Errorf("%w: invalid decision interval or lead", ErrInvalidBattle)
	}
	if scenario.Rules.MinimumDamage <= 0 || scenario.Rules.MinimumDamage > maxUnitStat || scenario.Rules.DefendAttackPercent < 0 || scenario.Rules.DefendAttackPercent > 100 || scenario.Rules.DefendDamageTakenPercent < 0 || scenario.Rules.DefendDamageTakenPercent > 100 {
		return fmt.Errorf("%w: invalid combat rules", ErrInvalidBattle)
	}
	if len(scenario.Factions) != 2 || scenario.Factions[0].ID == scenario.Factions[1].ID {
		return fmt.Errorf("%w: exactly two distinct factions are required", ErrInvalidBattle)
	}
	unitIDs := make(map[string]struct{})
	for _, faction := range scenario.Factions {
		if strings.TrimSpace(faction.ID) == "" || strings.TrimSpace(faction.Name) == "" || len(faction.Units) == 0 || len(faction.ScriptedPlans) == 0 {
			return fmt.Errorf("%w: faction identity, units, and scripted plans are required", ErrInvalidBattle)
		}
		for _, unit := range faction.Units {
			if strings.TrimSpace(unit.ID) == "" || unit.Health <= 0 || unit.Health > maxUnitStat ||
				unit.Attack < 0 || unit.Attack > maxUnitStat || unit.Defense < 0 || unit.Defense > maxUnitStat ||
				unit.Speed < 0 || unit.Speed > maxUnitStat {
				return fmt.Errorf("%w: invalid unit in faction %q", ErrInvalidBattle, faction.ID)
			}
			resolvedID := faction.ID + ":" + unit.ID
			if _, duplicate := unitIDs[resolvedID]; duplicate {
				return fmt.Errorf("%w: duplicate resolved unit ID %q", ErrInvalidBattle, resolvedID)
			}
			unitIDs[resolvedID] = struct{}{}
		}
	}
	return nil
}

func cloneScenario(scenario Scenario) Scenario {
	copy := scenario
	copy.Factions = make([]FactionSetup, len(scenario.Factions))
	for i, faction := range scenario.Factions {
		copy.Factions[i] = faction
		copy.Factions[i].Units = append([]UnitTemplate(nil), faction.Units...)
		copy.Factions[i].Traits = make([]controller.Trait, len(faction.Traits))
		for traitIndex, trait := range faction.Traits {
			copy.Factions[i].Traits[traitIndex] = controller.Trait{ID: trait.ID, ActionWeightModifiers: cloneInt64Map(trait.ActionWeightModifiers)}
		}
		copy.Factions[i].FallbackPlan = clonePlanProposal(faction.FallbackPlan)
		copy.Factions[i].ScriptedPlans = make([]controller.PlanProposal, len(faction.ScriptedPlans))
		for planIndex, plan := range faction.ScriptedPlans {
			copy.Factions[i].ScriptedPlans[planIndex] = clonePlanProposal(plan)
		}
	}
	return copy
}

func clonePlanProposal(plan controller.PlanProposal) controller.PlanProposal {
	return controller.PlanProposal{
		Name:          plan.Name,
		ActionWeights: cloneInt64Map(plan.ActionWeights),
		TargetBonuses: cloneInt64Map(plan.TargetBonuses),
	}
}

func cloneInt64Map(values map[string]int64) map[string]int64 {
	copy := make(map[string]int64, len(values))
	for key, value := range values {
		copy[key] = value
	}
	return copy
}

func cloneResult(result Result) Result {
	result.WinnerFactionID = cloneStringPointer(result.WinnerFactionID)
	result.Factions = append([]FactionStats(nil), result.Factions...)
	return result
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
