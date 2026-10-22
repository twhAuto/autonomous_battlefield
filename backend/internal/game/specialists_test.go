package game

import (
	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
	"testing"
)

func TestScoutRetreatAndRaiderVisiblePursuit(t *testing.T) {
	m := doctrineMatch(t)
	a, b := m.FactionIDs()[0], m.FactionIDs()[1]
	scout := Unit{ID: "scout", FactionID: a, Type: "scout", Scout: true, Health: 22, MaxHealth: 22, Position: Position{X: 4, Y: 4}, Movement: 3, Vision: 7, Range: 1}
	enemy := Unit{ID: "enemy", FactionID: b, Health: 40, Position: Position{X: 5, Y: 4}, Class: "infantry"}
	m.state.Units = []Unit{scout, enemy}
	old := HexDistance(scout.Position, enemy.Position)
	if events, ok := m.scoutRetreat(&m.state.Units[0]); !ok || len(events) != 1 || HexDistance(m.state.Units[0].Position, enemy.Position) <= old {
		t.Fatal("scout failed to retreat")
	}
	m.state.Units = []Unit{scout}
	if _, ok := m.scoutRetreat(&m.state.Units[0]); ok {
		t.Fatal("scout retreated from no enemy")
	}
	raider := scout
	raider.Scout = false
	raider.PreferredTargetClass = "siege"
	raider.Vision = 4
	siege := enemy
	siege.ID = "siege"
	siege.Class = "siege"
	siege.Position = Position{X: 7, Y: 4}
	m.state.Units = []Unit{raider, enemy, siege}
	if _, found := m.huntPreferred(&m.state.Units[0]); !found {
		t.Fatal("visible siege not pursued")
	}
	m.state.Units[0] = raider
	m.state.Units[0].Vision = 1
	if _, found := m.huntPreferred(&m.state.Units[0]); !found {
		t.Fatal("legacy vision limited open-map pursuit")
	}
	raider.Range = 4
	target := m.targetInRange(raider)
	if target == nil || target.id != "siege" {
		t.Fatal("raider did not prefer siege in range")
	}
}

func TestArmorPiercingOnlyAffectsUnitDefense(t *testing.T) {
	m := doctrineMatch(t)
	a, b := m.FactionIDs()[0], m.FactionIDs()[1]
	crossbow := Unit{ID: "crossbow", FactionID: a, Attack: 13, ArmorPiercing: 6}
	enemy := Unit{ID: "vanguard", FactionID: b, Health: 70, MaxHealth: 70, Defense: 7}
	target := &attackTarget{id: enemy.ID, kind: "unit", factionID: b, defense: 7, unit: &enemy}
	m.attackTarget(&crossbow, target, map[string]string{a: ActionAttack, b: ActionAttack})
	if enemy.Health != 58 {
		t.Fatalf("armor penetration damage: %d", enemy.Health)
	}
	building := Building{ID: "tower", FactionID: b, Health: 100, MaxHealth: 100, Defense: 7}
	m.attackTarget(&crossbow, &attackTarget{id: building.ID, kind: BuildingTower, factionID: b, defense: 7, building: &building}, map[string]string{})
	if building.Health != 94 {
		t.Fatal("penetration affected building")
	}
}

func TestEngineerRepairsWithCostCapAndInterruptions(t *testing.T) {
	m := doctrineMatch(t)
	a, b := m.FactionIDs()[0], m.FactionIDs()[1]
	engineer := Unit{ID: "engineer", Type: "engineer", FactionID: a, Class: "support", Health: 28, MaxHealth: 28, Position: Position{X: 3, Y: 4}, Movement: 1, RepairAmount: 4, RepairCost: 2, RepairCharges: 2}
	siege := Unit{ID: "ram", FactionID: a, Class: "siege", Health: 38, MaxHealth: 40, Position: Position{X: 4, Y: 4}}
	m.state.Units = []Unit{engineer, siege}
	m.factionState(a).Gold = 10
	events := m.runEngineer(&m.state.Units[0])
	if len(events) != 1 || events[0].Type != "unit_repaired" || m.state.Units[1].Health != 40 || m.factionState(a).Gold != 8 || m.state.Units[0].RepairCharges != 1 {
		t.Fatal("repair cost/cap")
	}
	// An adjacent hostile interrupts repair without consuming gold or charge.
	m.state.Units[1].Health = 30
	m.state.Units = append(m.state.Units, Unit{ID: "enemy", FactionID: b, Health: 10, Position: Position{X: 3, Y: 5}})
	if len(m.runEngineer(&m.state.Units[0])) != 0 || m.factionState(a).Gold != 8 {
		t.Fatal("enemy failed to interrupt")
	}
	m.state.Units = m.state.Units[:2]
	m.factions[a].activePlan.ReserveGold = 8
	if len(m.runEngineer(&m.state.Units[0])) != 0 {
		t.Fatal("repair violated reserve")
	}
	m.factions[a].activePlan.ReserveGold = 0
	m.runEngineer(&m.state.Units[0])
	if m.state.Units[0].RepairCharges != 0 || len(m.runEngineer(&m.state.Units[0])) != 0 {
		t.Fatal("repair charges not bounded")
	}
	// Buildings are eligible, castles, dead siege and soldiers are not.
	m.state.Units[0].RepairCharges = 1
	m.state.Units[1].Health = 0
	castle := m.castle(a)
	castle.Health = 1
	m.state.Buildings = append(m.state.Buildings, Building{ID: "economy", Type: BuildingEconomy, FactionID: a, Health: 55, MaxHealth: 60, Position: Position{X: 4, Y: 4}})
	events = m.runEngineer(&m.state.Units[0])
	if len(events) != 1 || events[0].Data["targetId"] != "economy" || castle.Health != 1 || m.state.Units[1].Health != 0 {
		t.Fatal("repair eligibility")
	}
}

func TestSpecializationChoicesAndFutureUnits(t *testing.T) {
	m := doctrineMatch(t)
	a := m.FactionIDs()[0]
	m.scenario.Ruleset.Upgrades = []Upgrade{
		{ID: "guard", Cost: 60, TargetUnit: "vanguard", ExclusiveGroup: "vanguard", DefenseBonus: 3},
		{ID: "assault", Cost: 60, TargetUnit: "vanguard", ExclusiveGroup: "vanguard", AttackBonus: 4},
		{ID: "reach", Cost: 60, TargetUnit: "herbalist", ExclusiveGroup: "herbalist", HealRangeBonus: 1},
		{ID: "siege", Cost: 60, TargetClass: "siege", ExclusiveGroup: "siege", MovementBonus: 1, BuildingDamageBonus: 8},
	}
	defs := UnitTypesByID(m.scenario.Ruleset)
	v := defs["vanguard"]
	r := defs["ranger"]
	m.state.Units = []Unit{m.factions[a].newUnit(v, Position{X: 3, Y: 4}, 0), m.factions[a].newUnit(r, Position{X: 4, Y: 4}, 0)}
	m.factionState(a).Gold = 500
	m.runUpgrades(a, controller.PlanProposal{UpgradeOrders: []string{"guard", "reach", "siege"}})
	if m.state.Units[0].Defense != v.Defense+3 || m.state.Units[1].Defense != r.Defense {
		t.Fatal("scoped effect leaked")
	}
	future := m.factions[a].newUnit(v, Position{}, 0)
	if future.Defense != v.Defense+3 {
		t.Fatal("future unit missed specialization")
	}
	if err := ValidateUpgradeOrder(m.scenario.Ruleset.Upgrades, m.factionState(a).OwnedUpgradeIDs, []string{"assault"}); err == nil {
		t.Fatal("conflicting owned branch accepted")
	}
	if err := ValidateUpgradeOrder(m.scenario.Ruleset.Upgrades, nil, []string{"guard", "assault"}); err == nil {
		t.Fatal("conflicting same-window choices accepted")
	}
	before := m.factionState(a).Gold
	skipped := m.runUpgrades(a, controller.PlanProposal{UpgradeOrders: []string{"assault"}})
	if len(skipped) != 1 || skipped[0].Type != "production_skipped" || skipped[0].Data["reason"] != "exclusive_choice_owned" {
		t.Fatalf("branch conflict reported incorrectly: %+v", skipped)
	}
	if m.factionState(a).Gold != before {
		t.Fatal("fallback bypassed branch lock")
	}
	healer := m.factions[a].newUnit(UnitType{ID: "herbalist", Class: "support", HealAmount: 3, Health: 25, Range: 1, Movement: 1}, Position{}, 0)
	if healer.HealRange != 2 || healer.HealAmount != 3 {
		t.Fatal("healer range branch")
	}
	siege := m.factions[a].newUnit(UnitType{ID: "ram", Class: "siege", Movement: 1, BuildingDamageBonus: 25}, Position{}, 0)
	if siege.Movement != 2 || siege.BuildingDamageBonus != 33 {
		t.Fatal("siege scope")
	}
}

func TestHealerReachTreatsTwoHexesAway(t *testing.T) {
	m := doctrineMatch(t)
	a := m.FactionIDs()[0]
	m.state.Units = []Unit{{ID: "herbalist", FactionID: a, Class: "support", Health: 25, MaxHealth: 25, HealAmount: 3, HealRange: 2, Position: Position{X: 3, Y: 4}}, {ID: "soldier", FactionID: a, Health: 20, MaxHealth: 40, Position: Position{X: 5, Y: 4}}}
	events := m.runHealer(&m.state.Units[0])
	if len(events) != 1 || events[0].Type != "unit_healed" || m.state.Units[1].Health != 23 {
		t.Fatal("reach specialization did not heal at range 2")
	}
}

func TestSpecialistDefinitionValidation(t *testing.T) {
	engineer := UnitType{ID: "engineer", Class: "support", RepairAmount: 4, RepairCost: 2, RepairCharges: 12}
	if !validSpecialist(engineer) {
		t.Fatal("valid engineer rejected")
	}
	for _, mutate := range []func(*UnitType){func(u *UnitType) { u.RepairCost = 0 }, func(u *UnitType) { u.RepairCharges = 0 }, func(u *UnitType) { u.Attack = 1 }, func(u *UnitType) { u.HealAmount = 3 }, func(u *UnitType) { u.Class = "infantry" }, func(u *UnitType) { u.BuildingPriority = true }, func(u *UnitType) { u.HealRange = 2 }, func(u *UnitType) { u.ArmorPiercing = -1 }} {
		u := engineer
		mutate(&u)
		if validSpecialist(u) {
			t.Fatalf("accepted %+v", u)
		}
	}
	if validSpecialist(UnitType{HealRange: 2}) || validSpecialist(UnitType{RepairCharges: 1}) {
		t.Fatal("orphan specialist fields accepted")
	}
	r := testScenario().Ruleset
	for _, u := range []Upgrade{
		{ID: "bad", TargetUnit: "missing"}, {ID: "bad", TargetClass: "missing"},
		{ID: "bad", TargetUnit: "vanguard", TargetClass: "infantry"}, {ID: "bad", MovementBonus: 1},
		{ID: "bad", TargetUnit: "vanguard", EconomyIncomeBonus: 2}, {ID: "bad", TargetUnit: "vanguard", HealAmountBonus: 2},
		{ID: "bad", TargetUnit: "vanguard", RangeBonus: -1},
	} {
		r.Upgrades = []Upgrade{u}
		if validateUpgradeEffects(r) == nil {
			t.Fatalf("accepted %+v", u)
		}
	}
}

func TestEngineerApproachesAndFollowsSiege(t *testing.T) {
	m := doctrineMatch(t)
	a := m.FactionIDs()[0]
	engineer := Unit{ID: "eng", FactionID: a, Position: Position{X: 3, Y: 4}, Movement: 1, RepairAmount: 4, RepairCost: 2, RepairCharges: 12}
	siege := Unit{ID: "ram", FactionID: a, Class: "siege", Health: 40, MaxHealth: 40, Position: Position{X: 5, Y: 4}}
	m.state.Units = []Unit{engineer, siege}
	if events := m.runEngineer(&m.state.Units[0]); len(events) != 1 || events[0].Type != "unit_moved" {
		t.Fatal("idle engineer did not follow siege")
	}
	m.state.Units[0] = engineer
	m.state.Units[1].Health = 30
	if events := m.runEngineer(&m.state.Units[0]); len(events) != 1 || events[0].Type != "unit_moved" {
		t.Fatal("engineer did not approach damaged siege")
	}
	m.state.Units = m.state.Units[:1]
	if events := m.runEngineer(&m.state.Units[0]); len(events) != 0 {
		t.Fatal("engineer moved without target")
	}
}
