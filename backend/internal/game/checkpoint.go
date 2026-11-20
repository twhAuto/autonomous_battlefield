// Captures and restores the complete resumable engine state at decision windows, so a
// replay can resume a match mid-battle instead of re-simulating from tick zero.

package game

import (
	"errors"
	"fmt"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/game/controller"
)

var ErrInvalidCheckpoint = errors.New("invalid match checkpoint")

// Checkpoint is the engine state after a transition, including the faction runtime
// that State does not carry. Restoring it with the battle's frozen scenario continues
// the match exactly as an uninterrupted run would.
type Checkpoint struct {
	State    State               `json:"state"`
	Factions []FactionCheckpoint `json:"factions"`
	Sequence uint64              `json:"sequence"`
	Started  bool                `json:"started"`
}

// FactionCheckpoint holds one faction's plan, upgrade and statistics runtime. Scoped
// upgrades are saved by ID in purchase order and resolved from the frozen ruleset.
// Checkpoints saved before per-type statistics restore with those lists empty.
type FactionCheckpoint struct {
	UnitTypes        []UnitTypeStats         `json:"unitTypes,omitempty"`
	BuildingTypes    []BuildingTypeStats     `json:"buildingTypes,omitempty"`
	Upgrades         []UpgradePurchase       `json:"upgrades,omitempty"`
	ActivePlan       controller.PlanProposal `json:"activePlan"`
	ScopedUpgradeIDs []string                `json:"scopedUpgradeIds,omitempty"`
	FactionID        string                  `json:"factionId"`
	ActiveSource     string                  `json:"activeSource"`
	PlanIndex        int                     `json:"planIndex"`
	UnitSequence     int                     `json:"unitSequence"`
	AttackBonus      int64                   `json:"attackBonus"`
	DefenseBonus     int64                   `json:"defenseBonus"`
	EconomyBonus     int64                   `json:"economyBonus"`
	DamageDealt      int64                   `json:"damageDealt"`
	DamageTaken      int64                   `json:"damageTaken"`
	AttackTicks      uint64                  `json:"attackTicks"`
	DefendTicks      uint64                  `json:"defendTicks"`
}

// Checkpoint returns an independent copy of the resumable match state. Attack aims are
// omitted because every tick recaptures them before use.
func (m *Match) Checkpoint() Checkpoint {
	factions := make([]FactionCheckpoint, 0, len(m.factions))
	for _, id := range m.sortedFactionIDs() {
		runtime := m.factions[id]
		upgradeIDs := make([]string, 0, len(runtime.scopedUpgrades))
		for _, upgrade := range runtime.scopedUpgrades {
			upgradeIDs = append(upgradeIDs, upgrade.ID)
		}
		factions = append(factions, FactionCheckpoint{
			ActivePlan: controller.ClonePlanProposal(runtime.activePlan), ScopedUpgradeIDs: upgradeIDs,
			FactionID: id, ActiveSource: runtime.activeSource, PlanIndex: runtime.planIndex, UnitSequence: runtime.unitSequence,
			AttackBonus: runtime.attackBonus, DefenseBonus: runtime.defenseBonus, EconomyBonus: runtime.economyBonus,
			DamageDealt: runtime.damageDealt, DamageTaken: runtime.damageTaken, AttackTicks: runtime.attackTicks, DefendTicks: runtime.defendTicks,
			UnitTypes: runtime.stats.unitList(), BuildingTypes: runtime.stats.buildingList(), Upgrades: runtime.stats.upgradeList(),
		})
	}
	return Checkpoint{State: CloneState(m.state), Factions: factions, Sequence: m.sequence, Started: m.started}
}

func (m *Match) checkpointPointer() *Checkpoint {
	checkpoint := m.Checkpoint()
	return &checkpoint
}

// RestoreMatch resumes a match from a checkpoint taken during the same battle. The
// restored match is never finished: callers stop at the battle's recorded final tick.
func RestoreMatch(id string, scenario Scenario, maxTicks uint64, checkpoint Checkpoint) (*Match, error) {
	if err := validateMatchScenario(id, scenario, maxTicks); err != nil {
		return nil, err
	}
	if checkpoint.State.BattleID != id || len(checkpoint.Factions) != len(scenario.Factions) {
		return nil, fmt.Errorf("%w: checkpoint does not belong to battle %q", ErrInvalidCheckpoint, id)
	}
	match := &Match{
		id: id, scenario: CloneScenario(scenario), maxTicks: maxTicks, state: CloneState(checkpoint.State),
		factions: make(map[string]*factionRuntime, len(scenario.Factions)), sequence: checkpoint.Sequence, started: checkpoint.Started,
	}
	saved := make(map[string]FactionCheckpoint, len(checkpoint.Factions))
	for _, faction := range checkpoint.Factions {
		saved[faction.FactionID] = faction
	}
	upgrades := UpgradesByID(match.scenario.Ruleset)
	for _, setup := range match.scenario.Factions {
		faction, ok := saved[setup.ID]
		if !ok || match.factionState(setup.ID) == nil {
			return nil, fmt.Errorf("%w: faction %q is missing", ErrInvalidCheckpoint, setup.ID)
		}
		runtime, err := newFactionRuntime(match.scenario, setup)
		if err != nil {
			return nil, err
		}
		if err := runtime.restore(faction, upgrades); err != nil {
			return nil, err
		}
		match.factions[setup.ID] = runtime
	}
	return match, nil
}

func (f *factionRuntime) restore(saved FactionCheckpoint, upgrades map[string]Upgrade) error {
	scoped := make([]Upgrade, 0, len(saved.ScopedUpgradeIDs))
	for _, id := range saved.ScopedUpgradeIDs {
		upgrade, ok := upgrades[id]
		if !ok {
			return fmt.Errorf("%w: unknown scoped upgrade %q", ErrInvalidCheckpoint, id)
		}
		scoped = append(scoped, upgrade)
	}
	f.activePlan, f.activeSource = controller.ClonePlanProposal(saved.ActivePlan), saved.ActiveSource
	f.planIndex, f.unitSequence, f.scopedUpgrades = saved.PlanIndex, saved.UnitSequence, scoped
	f.attackBonus, f.defenseBonus, f.economyBonus = saved.AttackBonus, saved.DefenseBonus, saved.EconomyBonus
	f.damageDealt, f.damageTaken, f.attackTicks, f.defendTicks = saved.DamageDealt, saved.DamageTaken, saved.AttackTicks, saved.DefendTicks
	f.stats = restoreTypeStatistics(saved.UnitTypes, saved.BuildingTypes, saved.Upgrades)
	return nil
}
