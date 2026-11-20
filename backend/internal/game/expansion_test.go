package game

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/game/controller"
)

func expansionMatch(t *testing.T) *Match {
	t.Helper()
	m, err := NewMatch("expansion", testScenario(), 100)
	if err != nil {
		t.Fatal(err)
	}
	m.state.Units = nil
	m.state.Tick = 1
	m.factionState("faction-a").Gold = 1000
	m.scenario.Ruleset.Buildings.MaximumBuildingCount = 4
	m.scenario.Ruleset.Structures = []BuildingType{
		{ID: BuildingTower, Kind: BuildingTower, Health: 130, Defense: 4, Vision: 4, Cost: 80, Attack: 12, Range: 3},
		{ID: BuildingWatchtower, Kind: BuildingWatchtower, Health: 50, Defense: 1, Vision: 6, Cost: 30},
		{ID: BuildingBarracks, Kind: BuildingBarracks, Health: 100, Defense: 3, Vision: 3, Cost: 90},
	}
	return m
}

func soldier(id, faction string, p Position) Unit {
	return Unit{ID: id, FactionID: faction, Type: "vanguard", Class: "infantry", Position: p, Health: 40, MaxHealth: 70, Attack: 9, Defense: 7, Speed: 4, Movement: 1, Range: 1, Vision: 3}
}

func healer(p Position) Unit {
	return Unit{ID: "herbalist", FactionID: "faction-a", Type: "herbalist", Class: "support", Position: p, Health: 25, MaxHealth: 25, Defense: 1, HealAmount: 3, Speed: 3, Movement: 1, Range: 1, Vision: 3}
}

func TestHealingPrioritiesExclusionsAndCap(t *testing.T) {
	m := expansionMatch(t)
	h := healer(Position{4, 5})
	a := soldier("a", h.FactionID, Position{5, 5})
	a.Health, a.MaxHealth = 10, 20
	b := soldier("b", h.FactionID, Position{4, 4})
	b.Health, b.MaxHealth = 20, 100
	siege := soldier("siege", h.FactionID, Position{3, 5})
	siege.Class, siege.Health = "siege", 1
	otherHealer := healer(Position{4, 6})
	otherHealer.ID, otherHealer.Health = "other-healer", 1
	dead := soldier("dead", h.FactionID, Position{5, 6})
	dead.Health = 0
	enemy := soldier("enemy", "faction-b", Position{5, 4})
	enemy.Health = 1
	m.state.Units = []Unit{h, a, b, siege, otherHealer, dead, enemy}
	events := m.runHealer(&m.state.Units[0])
	if len(events) != 1 || events[0].Type != "unit_healed" || events[0].Data["targetId"] != "b" || m.state.Units[2].Health != 23 {
		t.Fatalf("incorrect treatment: %+v", events)
	}
	m.state.Units[1].Health, m.state.Units[1].MaxHealth = 69, 70
	m.state.Units[2].Health = m.state.Units[2].MaxHealth
	events = m.runHealer(&m.state.Units[0])
	if events[0].Data["amount"] != int64(1) || m.state.Units[1].Health != 70 {
		t.Fatal("overhealing")
	}
	for _, i := range []int{3, 4, 5, 6} {
		if m.state.Units[i].Health != []Unit{siege, otherHealer, dead, enemy}[i-3].Health {
			t.Fatal("ineligible patient treated")
		}
	}
	m.state.Units[1].Health, m.state.Units[2].Health, m.state.Units[2].MaxHealth = 35, 35, 70
	events = m.runHealer(&m.state.Units[0])
	if events[0].Data["targetId"] != "a" {
		t.Fatal("tie is not stable")
	}
	before := m.state.Units[1].Health
	m.state.Units[0].ReadyAtTick = m.state.Tick + 1
	m.state.Units = m.state.Units[:2]
	m.state.Units[1].ReadyAtTick = m.state.Tick + 1
	m.runUnits(nil)
	if m.state.Units[1].Health != before {
		t.Fatal("new healer acted early")
	}
}

func TestHealerFollowsAndNeverAttacks(t *testing.T) {
	m := expansionMatch(t)
	h := healer(Position{3, 5})
	a := soldier("patient", h.FactionID, Position{6, 5})
	m.state.Units = []Unit{h, a}
	events, _ := m.runUnit(&m.state.Units[0], nil)
	if len(events) != 1 || events[0].Type != "unit_moved" || HexDistance(m.state.Units[0].Position, a.Position) >= HexDistance(h.Position, a.Position) || m.state.Units[1].Health != a.Health {
		t.Fatalf("follow: %+v", events)
	}
	m.state.Units = []Unit{h, soldier("enemy", "faction-b", Position{4, 5})}
	if events, _ := m.runUnit(&m.state.Units[0], nil); len(events) != 0 || m.state.Units[1].Health != 40 {
		t.Fatal("healer attacked enemy")
	}
	a.Health = a.MaxHealth
	m.state.Units = []Unit{h, a}
	if events := m.runHealer(&m.state.Units[0]); len(events) != 1 || events[0].Type != "unit_moved" {
		t.Fatal("healthy escort did not move")
	}
	m.state.Units = []Unit{h, a}
	for _, p := range Neighbors(h.Position) {
		m.state.Buildings = append(m.state.Buildings, Building{Position: p, Health: 10})
	}
	if events := m.runHealer(&m.state.Units[0]); len(events) != 0 {
		t.Fatal("healer crossed occupied cells")
	}
}

func TestHealingDoesNotScaleWithArsenalAndClonesAreIsolated(t *testing.T) {
	m := expansionMatch(t)
	h := healer(Position{4, 5})
	m.state.Units = []Unit{h}
	m.runUpgrades(h.FactionID, controller.PlanProposal{UpgradeOrders: []string{"arsenal", "plating"}})
	if m.state.Units[0].Attack != 0 || m.state.Units[0].HealAmount != 3 || m.state.Units[0].Defense != 3 {
		t.Fatalf("invalid support upgrade: %+v", m.state.Units[0])
	}
	u := m.factions[h.FactionID].newUnit(UnitType{ID: "herbalist", Class: "support", Health: 25, HealAmount: 3, Defense: 1}, Position{}, 0)
	if u.Attack != 0 || u.HealAmount != 3 || u.Defense != 3 {
		t.Fatal("new support upgrade mismatch")
	}
	p := Position{5, 5}
	m.state.Units[0].HealTargetPosition = &p
	m.state.Buildings[0].AttackTargetPosition = &p
	copy := m.State()
	copy.Units[0].HealTargetPosition.X = 99
	copy.Buildings[0].AttackTargetPosition.X = 99
	if p.X != 5 {
		t.Fatal("snapshot aliases action positions")
	}
	m.clearUnitActions()
	if m.state.Units[0].HealTargetPosition != nil || m.state.Buildings[0].AttackTargetPosition != nil {
		t.Fatal("stale actions")
	}
}

func TestSiegeAndClassDamage(t *testing.T) {
	m := expansionMatch(t)
	catapult := soldier("catapult", "faction-a", Position{4, 5})
	catapult.Class, catapult.Attack, catapult.BuildingDamageBonus, catapult.Range, catapult.MinimumRange, catapult.BuildingPriority = "siege", 4, 20, 5, 2, true
	tower := Building{ID: "tower", FactionID: "faction-b", Type: BuildingTower, Position: Position{8, 5}, Health: 130, Defense: 4}
	m.state.Buildings = append(m.state.Buildings, tower)
	m.state.Units = []Unit{catapult, soldier("enemy", "faction-b", Position{6, 5})}
	events, _ := m.runUnit(&m.state.Units[0], nil)
	if events[0].Data["targetId"] != "tower" || events[0].Data["damage"] != int64(20) {
		t.Fatalf("siege target/damage: %+v", events)
	}
	m.state.Units[1].Position = Position{5, 5}
	if inAttackRange(catapult, m.state.Units[1].Position) {
		t.Fatal("minimum range ignored")
	}
	events, _ = m.runUnit(&m.state.Units[0], nil)
	if len(events) != 1 || events[0].Type != "unit_moved" || HexDistance(m.state.Units[0].Position, m.state.Units[1].Position) < 2 {
		t.Fatalf("catapult did not retreat: %+v", events)
	}
	pike := soldier("pike", "faction-a", Position{4, 5})
	pike.Attack, pike.ClassDamageBonus, pike.BonusAgainstClass = 8, 12, "mobile"
	striker := soldier("striker", "faction-b", Position{5, 5})
	striker.Class, striker.Defense = "mobile", 3
	m.state.Units = []Unit{pike, striker}
	events, _ = m.runUnit(&m.state.Units[0], nil)
	if events[0].Data["damage"] != int64(17) {
		t.Fatalf("class bonus: %+v", events)
	}
	m.state.Units[1].Class = "infantry"
	events, _ = m.runUnit(&m.state.Units[0], nil)
	if events[0].Data["damage"] != int64(5) {
		t.Fatal("class bonus leaked")
	}
}

func TestSiegeApproachesStructuresAndBlockedRetreat(t *testing.T) {
	m := expansionMatch(t)
	u := soldier("ram", "faction-a", Position{3, 5})
	u.BuildingPriority = true
	m.state.Units = []Unit{u}
	m.state.Buildings = append(m.state.Buildings, Building{ID: "tower", Type: BuildingTower, FactionID: "faction-b", Health: 100, Position: Position{3, 2}})
	events := m.moveSiege(&m.state.Units[0])
	if len(events) != 1 || HexDistance(m.state.Units[0].Position, Position{3, 2}) >= HexDistance(u.Position, Position{3, 2}) {
		t.Fatal("ram did not approach tower")
	}
	u.MinimumRange, u.Range = 2, 5
	m.state.Units = []Unit{u, soldier("enemy", "faction-b", Position{4, 5})}
	for _, p := range Neighbors(u.Position) {
		m.state.Buildings = append(m.state.Buildings, Building{Health: 10, Position: p})
	}
	if events, moved := m.retreatFromCloseEnemies(&m.state.Units[0]); moved || len(events) > 0 {
		t.Fatal("retreat crossed blockers")
	}
}

func TestTypedConstructionLimitsAndRebuilding(t *testing.T) {
	m := expansionMatch(t)
	orders := []controller.ConstructionOrder{{BuildingType: BuildingTower, Position: controller.Coordinate{X: 2, Y: 2}}}
	plan := controller.PlanProposal{Name: "fortify", ConstructionOrders: &orders}
	if err := validateProductionPlan(m.scenario, m.scenario.Factions[0], plan); err != nil {
		t.Fatal(err)
	}
	events := m.runConstruction("faction-a", plan)
	if len(events) != 1 || events[0].Type != "building_constructed" || m.factionState("faction-a").Gold != 920 {
		t.Fatal("typed purchase failed")
	}
	if _, reason := m.buildStructureAt("faction-a", BuildingTower, Position{2, 2}, 0); reason != "site_occupied" {
		t.Fatal(reason)
	}
	m.state.Buildings[len(m.state.Buildings)-1].Health = 0
	if _, reason := m.buildStructureAt("faction-a", BuildingWatchtower, Position{2, 2}, 0); reason != "" {
		t.Fatal(reason)
	}
	if _, reason := m.buildStructureAt("faction-a", "missing", Position{2, 8}, 0); reason != "unsupported_building" {
		t.Fatal(reason)
	}
	if _, reason := m.buildStructureAt("faction-a", BuildingBarracks, Position{2, 8}, 1000); reason != "reserve_required" {
		t.Fatal(reason)
	}
	m.scenario.Ruleset.Buildings.MaximumBuildingCount = 1
	if _, reason := m.buildStructureAt("faction-a", BuildingEconomy, Position{2, 8}, 0); reason != "maximum_reached" {
		t.Fatal("economy bypassed shared cap")
	}
	orders[0].Position = controller.Coordinate{X: 12, Y: 2}
	if ConstructionReason(m.scenario.Map, m.state, "faction-a", Position{12, 2}) != "outside_build_range" {
		t.Fatal("out-of-range construction accepted")
	}
	orders[0].Position, orders[0].BuildingType = controller.Coordinate{X: 2, Y: 2}, "missing"
	if validateProductionPlan(m.scenario, m.scenario.Factions[0], plan) == nil {
		t.Fatal("unknown structure accepted")
	}
	empty := []controller.ConstructionOrder{}
	if p := m.normalizedFactionPlan("faction-a", controller.PlanProposal{Name: "save", ConstructionOrders: &empty}); len(constructionOrders(p)) != 0 {
		t.Fatal("empty orders replaced with defaults")
	}
}

func TestTowersDelayKillAndDoNotAttackBuildings(t *testing.T) {
	m := expansionMatch(t)
	b, reason := m.buildStructureAt("faction-a", BuildingTower, Position{2, 2}, 0)
	if reason != "" {
		t.Fatal(reason)
	}
	enemy := soldier("enemy", "faction-b", Position{3, 2})
	enemy.Health, enemy.Defense = 5, 0
	m.state.Units = []Unit{enemy}
	if events := m.runTowers(nil); len(events) != 0 {
		t.Fatal("tower acted during construction tick")
	}
	m.state.Tick++
	events := m.runTowers(nil)
	if len(events) != 2 || events[0].Type != "building_attacked" || events[0].Data["damage"] != int64(5) || events[1].Type != "unit_destroyed" || m.factionState("faction-b").UnitsLost != 1 {
		t.Fatalf("tower attack: %+v", events)
	}
	if m.state.Buildings[len(m.state.Buildings)-1].AttackTargetID != "enemy" {
		t.Fatal("missing tower action")
	}
	if events := m.runTowers(nil); len(events) != 0 {
		t.Fatal("tower attacked a dead unit/building")
	}
	if b.ReadyAtTick != 2 {
		t.Fatal("wrong activation tick")
	}
}

func TestBarracksRecruitmentSourcesAndSharedLimit(t *testing.T) {
	m := expansionMatch(t)
	b, reason := m.buildStructureAt("faction-a", BuildingBarracks, Position{2, 2}, 0)
	if reason != "" {
		t.Fatal(reason)
	}
	orders := []controller.RecruitmentOrder{{UnitType: "ranger", Count: 1, SourceBuildingID: b.ID}, {UnitType: "striker", Count: 1}}
	plan := controller.PlanProposal{Name: "reinforce", RecruitmentOrders: &orders}
	events := m.runRecruitment("faction-a", plan)
	if events[0].Data["reason"] != "source_unavailable" || len(m.castle("faction-a").TrainingQueue) != 1 {
		t.Fatal("inactive source not skipped")
	}
	m.state.Tick++
	m.runTraining()
	m.state.Units = nil
	m.runRecruitment("faction-a", plan)
	m.state.Tick++
	m.runTraining()
	if len(m.state.Units) != 2 || HexDistance(m.state.Units[0].Position, b.Position) != 1 || HexDistance(m.state.Units[1].Position, m.castle("faction-a").Position) != 1 {
		t.Fatal("incorrect deployment origins")
	}
	if m.recruitmentSource("faction-b", b.ID) != nil {
		t.Fatal("enemy source usable")
	}
	m.recruitmentSource("faction-a", b.ID).Health = 0
	events = m.runRecruitment("faction-a", plan)
	if events[0].Data["reason"] != "source_unavailable" {
		t.Fatal("destroyed source usable")
	}
	orders[0].Count, orders[1].Count = 4, 3
	if controller.ValidatePlanProposal(plan, []string{ActionAttack}) == nil {
		t.Fatal("shared six-unit limit ignored")
	}
}

func TestExpansionVisibilityAndSnapshotRoundTrip(t *testing.T) {
	m := expansionMatch(t)
	m.state.Units = []Unit{healer(Position{4, 5})}
	far := Position{13, 10}
	m.state.Units[0].HealTargetID, m.state.Units[0].HealTargetPosition = "hidden", &far
	m.state.Buildings[0].AttackTargetID, m.state.Buildings[0].AttackTargetPosition = "hidden", &far
	view, _ := m.View("faction-a")
	if view.State.Units[0].HealTargetID != "hidden" || view.State.Buildings[0].AttackTargetPosition == nil {
		t.Fatal("open view lost action positions")
	}
	if m.state.Units[0].HealTargetPosition == nil {
		t.Fatal("view mutated authoritative state")
	}
	before, _ := m.View("faction-a")
	b, _ := m.buildStructureAt("faction-a", BuildingWatchtower, Position{2, 2}, 0)
	after, _ := m.View("faction-a")
	if b.BuildRange != DefaultBuildRange || len(after.VisiblePositions) != len(before.VisiblePositions) {
		t.Fatal("legacy structure changed open map visibility")
	}
	data, _ := json.Marshal(m.Scenario())
	var restored Scenario
	if err := json.Unmarshal(data, &restored); err != nil || !reflect.DeepEqual(restored.Ruleset.Structures, m.scenario.Ruleset.Structures) {
		t.Fatal("structure snapshot lost")
	}
	data, _ = json.Marshal(m.State())
	var state State
	if err := json.Unmarshal(data, &state); err != nil || !reflect.DeepEqual(state, m.State()) {
		t.Fatal("state roundtrip failed")
	}
}

func TestExpansionRulesRejectInvalidCapabilities(t *testing.T) {
	m := expansionMatch(t)
	u := UnitType{ID: "herbalist", Class: "support", Cost: 40, Health: 25, HealAmount: 3, Movement: 1, Range: 1, Vision: 3}
	if !validUnitType(u) {
		t.Fatal("valid healer rejected")
	}
	for _, mutate := range []func(*UnitType){
		func(u *UnitType) { u.Class = "unknown" }, func(u *UnitType) { u.Attack = 1 },
		func(u *UnitType) { u.HealAmount = -1 }, func(u *UnitType) { u.Range = 2 },
		func(u *UnitType) { u.BuildingPriority = true }, func(u *UnitType) { u.MinimumRange = 2 },
		func(u *UnitType) { u.BuildingDamageBonus = -1 }, func(u *UnitType) { u.ClassDamageBonus = 3 },
	} {
		copy := u
		mutate(&copy)
		if validUnitType(copy) {
			t.Fatalf("invalid capabilities accepted: %+v", copy)
		}
	}
	for _, mutate := range []func(*BuildingType){
		func(b *BuildingType) { b.ID = "custom" }, func(b *BuildingType) { b.Kind = "unknown" },
		func(b *BuildingType) { b.Attack = 0 }, func(b *BuildingType) { b.Range = 0 },
		func(b *BuildingType) { b.Income = 1 }, func(b *BuildingType) { b.Cost = 0 },
	} {
		copy := m.scenario.Ruleset.Structures[0]
		mutate(&copy)
		if ValidStructure(copy) {
			t.Fatalf("invalid structure accepted: %+v", copy)
		}
	}
	r := m.scenario.Ruleset
	r.Buildings.MaximumBuildingCount = 0
	if ValidateRuleset(r) != nil {
		t.Fatal("uncapped structures rejected")
	}
	r = m.scenario.Ruleset
	r.Buildings.GoldMineReserve = -1
	if ValidateRuleset(r) == nil {
		t.Fatal("negative gold mine reserve accepted")
	}
	r.Buildings.GoldMineReserve = MaximumGoldMineReserve + 1
	if ValidateRuleset(r) == nil {
		t.Fatal("oversized gold mine reserve accepted")
	}
	r = m.scenario.Ruleset
	r.Buildings.MaximumBuildingCount = MaximumBuildingCountLimit + 1
	if ValidateRuleset(r) == nil {
		t.Fatal("oversized building cap accepted")
	}
}

func TestMultipleHealersRetargetAfterPatientIsFull(t *testing.T) {
	m := expansionMatch(t)
	h := healer(Position{4, 5})
	h2 := healer(Position{4, 4})
	h2.ID = "herbalist-2"
	a := soldier("a", "faction-a", Position{5, 4})
	a.Health = 69
	b := soldier("b", "faction-a", Position{3, 4})
	b.Health = 70
	m.state.Units = []Unit{h, h2, a, b}
	m.runHealer(&m.state.Units[0])
	events := m.runHealer(&m.state.Units[1])
	for _, e := range events {
		if e.Type == "unit_healed" {
			t.Fatal("second healer healed a full patient")
		}
	}
	if m.state.Units[2].Health != 70 {
		t.Fatal("healing exceeded cap")
	}
}
