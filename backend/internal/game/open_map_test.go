package game

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
)

func queueUnits(t *testing.T, m *Match, source string, types ...string) {
	t.Helper()
	orders := []controller.RecruitmentOrder{}
	for _, kind := range types {
		orders = append(orders, controller.RecruitmentOrder{UnitType: kind, Count: 1, SourceBuildingID: source})
	}
	events := m.runRecruitment("faction-a", controller.PlanProposal{RecruitmentOrders: &orders})
	for _, e := range events {
		if e.Type != "unit_queued" {
			t.Fatalf("queue failed: %+v", events)
		}
	}
}

func TestTrainingFIFOAndBlockedExit(t *testing.T) {
	m := doctrineMatch(t)
	m.scenario.Ruleset.Units[0].TrainingTicks = 2
	m.scenario.Ruleset.Units[1].TrainingTicks = 3
	m.factionState("faction-a").Gold = 500
	queueUnits(t, m, "", "vanguard", "striker")
	castle := m.castle("faction-a")
	paid := m.factionState("faction-a").Gold
	m.runTraining()
	if len(m.state.Units) != 0 || castle.TrainingQueue[0].RemainingTicks != 2 {
		t.Fatal("trained on purchase tick")
	}
	m.state.Tick = 1
	m.runTraining()
	if castle.TrainingQueue[0].RemainingTicks != 1 || castle.TrainingQueue[1].RemainingTicks != 3 {
		t.Fatal("followers trained concurrently")
	}
	for i, p := range Neighbors(castle.Position) {
		m.state.Units = append(m.state.Units, Unit{ID: string(rune('a' + i)), FactionID: "faction-b", Position: p, Health: 10})
	}
	for tick := uint64(2); tick <= 5; tick++ {
		m.state.Tick = tick
		if len(m.runTraining()) != 0 {
			t.Fatal("deployed through occupied exit")
		}
	}
	if !castle.TrainingBlocked || castle.TrainingQueue[0].RemainingTicks != 0 || len(castle.TrainingQueue) != 2 || m.factionState("faction-a").Gold != paid {
		t.Fatal("blocked queue lost progress or charged again")
	}
	// Free exactly one exit. The head leaves; the follower retains its full time.
	freed := m.state.Units[0].Position
	m.state.Units = m.state.Units[1:]
	m.state.Tick = 6
	events := m.runTraining()
	if len(events) != 1 || m.state.Units[len(m.state.Units)-1].Position != freed || castle.TrainingBlocked || len(castle.TrainingQueue) != 1 || castle.TrainingQueue[0].RemainingTicks != 3 {
		t.Fatal("FIFO unblock failed")
	}
	if m.state.Units[len(m.state.Units)-1].ReadyAtTick != 7 {
		t.Fatal("unit acts during deployment tick")
	}
	m.state.Units = nil
	for tick := uint64(7); tick < 9; tick++ {
		m.state.Tick = tick
		m.runTraining()
		if len(m.state.Units) != 0 {
			t.Fatal("wrong unit-specific time")
		}
	}
	m.state.Tick = 9
	m.runTraining()
	if len(m.state.Units) != 1 || m.state.Units[0].Type != "striker" || m.factionState("faction-a").UnitsStarted != 2 {
		t.Fatal("second opening unit not deployed")
	}
}

func TestTrainingQueueCapacityCopiesAndDestroyedSource(t *testing.T) {
	m := doctrineMatch(t)
	m.factionState("faction-a").Gold = 1000
	for range 2 {
		queueUnits(t, m, "", "striker", "striker", "striker", "striker", "striker", "striker")
	}
	before := m.factionState("faction-a").Gold
	orders := []controller.RecruitmentOrder{{UnitType: "striker", Count: 1}}
	events := m.runRecruitment("faction-a", controller.PlanProposal{RecruitmentOrders: &orders})
	if events[0].Data["reason"] != "queue_full" || m.factionState("faction-a").Gold != before {
		t.Fatal("full queue charged")
	}
	copy := m.State()
	copy.Buildings[0].TrainingQueue[0].RemainingTicks = 999
	view, _ := m.View("faction-b")
	view.State.Buildings[0].TrainingQueue[0].UnitType = "changed"
	if m.castle("faction-a").TrainingQueue[0].RemainingTicks == 999 || m.castle("faction-a").TrainingQueue[0].UnitType == "changed" {
		t.Fatal("queue aliases view")
	}
	payload, err := json.Marshal(m.State())
	if err != nil {
		t.Fatal(err)
	}
	var restored State
	if err = json.Unmarshal(payload, &restored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(restored, m.State()) {
		t.Fatal("queue snapshot roundtrip")
	}
	// Resume from the serialized state and compare the next training transition.
	next := doctrineMatch(t)
	next.state = restored
	next.sequence = m.sequence
	m.state.Tick = 1
	next.state.Tick = 1
	if !reflect.DeepEqual(m.runTraining(), next.runTraining()) {
		t.Fatal("restored queue timing differs")
	}
	m.castle("faction-a").Health = 0
	m.state.Tick++
	m.runTraining()
	if len(m.castle("faction-a").TrainingQueue) != 0 || m.factionState("faction-a").Gold != before {
		t.Fatal("destroyed queue not discarded without refund")
	}
}

func TestConstructionRangeSpacingOccupancyAndTerrain(t *testing.T) {
	m := doctrineMatch(t)
	m.state.OpeningComplete = true
	m.factionState("faction-a").Gold = 1000
	m.castle("faction-a").BuildRange = 3
	setTerrain := func(p Position, symbol byte) {
		row := []byte(m.scenario.Map.Rows[p.Y])
		row[p.X] = symbol
		m.scenario.Map.Rows[p.Y] = string(row)
	}
	site := Position{3, 5}
	cases := []struct {
		name string
		site Position
		want string
	}{
		{"castle", Position{1, 5}, "site_occupied"}, {"neighbor", Position{2, 5}, "building_too_close"},
		{"edge of range", Position{4, 5}, ""}, {"outside", Position{5, 5}, "outside_build_range"},
		{"off map", Position{-1, 0}, "invalid_terrain"}, {"enemy", Position{12, 5}, "building_too_close"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ConstructionReason(m.scenario.Map, m.state, "faction-a", tc.site); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
	setTerrain(site, 'f')
	if ConstructionReason(m.scenario.Map, m.state, "faction-a", site) != "invalid_terrain" {
		t.Fatal("forest construction")
	}
	setTerrain(site, 'g')
	m.state.Units = []Unit{{Position: site, Health: 1}}
	if ConstructionReason(m.scenario.Map, m.state, "faction-a", site) != "site_occupied" {
		t.Fatal("built on troop")
	}
	m.state.Units = nil
	snap, _ := m.DecisionSnapshot("faction-a")
	found := false
	for _, p := range snap.LegalSites {
		if p.X == site.X && p.Y == site.Y {
			found = true
		}
	}
	if !found {
		t.Fatal("deposit not legal")
	}
	b, reason := m.buildEconomyBuildingAt("faction-a", site, 0)
	if reason != "" || b.Income != m.scenario.Ruleset.Buildings.EconomyIncome+GoldDepositIncomeBonus {
		t.Fatal("deposit bonus", reason)
	}
	if ConstructionReason(m.scenario.Map, m.state, "faction-a", Position{5, 5}) != "" {
		t.Fatal("building did not extend range")
	}
	before := m.factionState("faction-a").Gold
	if _, reason = m.buildEconomyBuildingAt("faction-a", Position{4, 5}, 0); reason != "building_too_close" || m.factionState("faction-a").Gold != before {
		t.Fatal("spacing charged")
	}
	m.state.Buildings[len(m.state.Buildings)-1].Health = 0
	if ConstructionReason(m.scenario.Map, m.state, "faction-a", Position{5, 5}) != "outside_build_range" {
		t.Fatal("destroyed building extends range")
	}
}

func TestForestCoverAppliesAfterArmorPiercing(t *testing.T) {
	hit := func(terrain byte) int64 {
		m := expansionMatch(t)
		attacker := soldier("attacker", "faction-a", Position{4, 5})
		attacker.Attack = 20
		attacker.ArmorPiercing = 100
		target := soldier("target", "faction-b", Position{5, 5})
		row := []byte(m.scenario.Map.Rows[5])
		row[5] = terrain
		m.scenario.Map.Rows[5] = string(row)
		m.state.Units = []Unit{attacker, target}
		selected := m.targetInRange(attacker)
		events, _ := m.attackTarget(&m.state.Units[0], selected, nil)
		return events[0].Data["damage"].(int64)
	}
	if hit('.')-hit('f') != ForestDefenseBonus {
		t.Fatal("cover bypassed or not applied")
	}
}

// Old pads remain construction terrain, but cannot become movement/spawn exits.
func TestLegacyBuildPadsRemainImpassable(t *testing.T) {
	m := Map{Width: 5, Height: 1, Rows: []string{".bfg#"}}
	for x, symbol := range m.Rows[0] {
		p := Position{x, 0}
		wantWalk := symbol == '.' || symbol == 'f' || symbol == 'g'
		wantBuild := symbol == '.' || symbol == 'b' || symbol == 'g'
		if m.Traversable(p) != wantWalk || m.Buildable(p) != wantBuild {
			t.Fatalf("terrain %c: traversable=%v buildable=%v", symbol, m.Traversable(p), m.Buildable(p))
		}
	}
}
