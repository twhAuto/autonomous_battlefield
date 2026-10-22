package game

import (
	"reflect"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
)

func continuousMatch(t *testing.T) *Match {
	t.Helper()
	s := specializedMatch(t).Scenario()
	s.Ruleset.ContinuousProduction = true
	for i := range s.Ruleset.Units {
		s.Ruleset.Units[i].TrainingTicks = 2
	}
	m, err := NewMatch("continuous", s, 100)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func productionPlan(unit string, enabled bool) controller.PlanProposal {
	orders := []controller.ProductionOrder{{BuildingID: "faction-a:castle", UnitType: unit, Enabled: enabled}}
	return controller.PlanProposal{Name: "repeat", ProductionOrders: &orders, SpendOrder: []string{"upgrades"}}
}

func TestContinuousOpeningAndRepeatedPurchases(t *testing.T) {
	m := continuousMatch(t)
	plan := productionPlan("vanguard", true)
	if _, err := m.AdvanceWithPlans(map[string]controller.PlanProposal{"faction-a": plan}); err != nil {
		t.Fatal(err)
	}
	b := m.castle("faction-a")
	cost := UnitTypesByID(m.scenario.Ruleset)["vanguard"].Cost
	if len(b.TrainingQueue) != 1 || m.factionState("faction-a").Gold != 180-cost {
		t.Fatal("opening must pay for exactly one unit")
	}
	m.state.Tick = 1
	m.runTraining()
	m.runContinuousProduction()
	if b.TrainingQueue[0].RemainingTicks != 1 {
		t.Fatal("training duration ignored")
	}
	m.state.Tick = 2
	m.runTraining()
	m.runContinuousProduction()
	if len(b.TrainingQueue) != 1 || b.TrainingQueue[0].RemainingTicks != 2 || m.factionState("faction-a").Gold != 180-2*cost || m.factionState("faction-a").UnitsStarted != 1 {
		t.Fatal("did not restart once after deployment")
	}
	if m.state.Units[0].ReadyAtTick != 3 {
		t.Fatal("deployed unit activated early")
	}
}

func TestContinuousWaitsForIncomeAndReserve(t *testing.T) {
	m := continuousMatch(t)
	plan := productionPlan("vanguard", true)
	plan.ReserveGold = 10
	m.factions["faction-a"].activePlan = plan
	m.applyBuildingProduction(plan)
	cost := UnitTypesByID(m.scenario.Ruleset)["vanguard"].Cost
	m.factionState("faction-a").Gold = cost + 9
	b := m.castle("faction-a")
	if len(m.runContinuousProduction()) != 0 || !b.Production.WaitingForGold || len(b.TrainingQueue) != 0 {
		t.Fatal("spent protected gold")
	}
	m.runEconomy(false)
	if len(m.runContinuousProduction()) != 1 || b.Production.WaitingForGold || !b.Production.Enabled {
		t.Fatal("income did not resume training")
	}
	paid := m.factionState("faction-a").GoldSpent
	m.runContinuousProduction()
	if paid != m.factionState("faction-a").GoldSpent {
		t.Fatal("paid twice")
	}
}

func TestContinuousToggleAndSelectionKeepPaidUnit(t *testing.T) {
	m := continuousMatch(t)
	m.applyBuildingProduction(productionPlan("vanguard", true))
	m.runContinuousProduction()
	b := m.castle("faction-a")
	m.applyBuildingProduction(productionPlan("striker", true))
	if b.TrainingQueue[0].UnitType != "vanguard" {
		t.Fatal("selection replaced paid unit")
	}
	m.state.Tick = 1
	m.runTraining()
	m.state.Tick = 2
	m.runTraining()
	m.runContinuousProduction()
	if b.TrainingQueue[0].UnitType != "striker" {
		t.Fatal("next selection not applied")
	}
	m.applyBuildingProduction(productionPlan("", false))
	paid := m.factionState("faction-a").GoldSpent
	m.state.Tick = 3
	m.runTraining()
	m.state.Tick = 4
	m.runTraining()
	m.runContinuousProduction()
	if b.Production.Enabled || b.Production.UnitType != "striker" || len(b.TrainingQueue) != 0 || paid != m.factionState("faction-a").GoldSpent {
		t.Fatal("off must finish paid unit without restart")
	}
	m.applyBuildingProduction(productionPlan("striker", true))
	m.runContinuousProduction()
	if len(b.TrainingQueue) != 1 {
		t.Fatal("reenabling did not start")
	}
}

func TestContinuousBlockedExitAndDestruction(t *testing.T) {
	m := continuousMatch(t)
	m.applyBuildingProduction(productionPlan("vanguard", true))
	m.runContinuousProduction()
	b := m.castle("faction-a")
	for i, pos := range freeNeighbors(m.scenario.Map, b.Position, m.occupied()) {
		u := soldier("block", "faction-a", pos)
		u.ID += string(rune('a' + i))
		m.state.Units = append(m.state.Units, u)
	}
	paid := m.factionState("faction-a").GoldSpent
	for tick := uint64(1); tick <= 4; tick++ {
		m.state.Tick = tick
		m.runTraining()
		m.runContinuousProduction()
	}
	if !b.TrainingBlocked || b.TrainingQueue[0].RemainingTicks != 0 || paid != m.factionState("faction-a").GoldSpent {
		t.Fatal("blocked source advanced or charged")
	}
	m.state.Units = nil
	m.state.Tick++
	m.runTraining()
	m.runContinuousProduction()
	if b.TrainingBlocked || len(b.TrainingQueue) != 1 || len(m.state.Units) != 1 {
		t.Fatal("cleared exit did not deploy and resume")
	}
	paid = m.factionState("faction-a").GoldSpent
	b.Health = 0
	m.runTraining()
	m.runContinuousProduction()
	if len(b.TrainingQueue) != 0 || paid != m.factionState("faction-a").GoldSpent {
		t.Fatal("destroyed source trained or refunded")
	}
}

func TestContinuousOrdersValidationAndIsolation(t *testing.T) {
	m := continuousMatch(t)
	for _, tc := range []struct {
		name  string
		order controller.ProductionOrder
	}{
		{"enemy", controller.ProductionOrder{BuildingID: "faction-b:castle", UnitType: "vanguard", Enabled: true}},
		{"missing", controller.ProductionOrder{BuildingID: "missing", UnitType: "vanguard", Enabled: true}},
		{"wrong roster", controller.ProductionOrder{BuildingID: "faction-a:castle", UnitType: "ranger", Enabled: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			orders := []controller.ProductionOrder{tc.order}
			plan := controller.PlanProposal{Name: "bad", ProductionOrders: &orders}
			before := m.State()
			if m.applyPlans(map[string]controller.PlanProposal{"faction-a": plan}) == nil || !reflect.DeepEqual(before, m.State()) {
				t.Fatal("invalid plan mutated state")
			}
		})
	}
	plan := productionPlan("vanguard", true)
	if err := m.applyPlans(map[string]controller.PlanProposal{"faction-a": plan}); err != nil {
		t.Fatal(err)
	}
	copy := m.State()
	copy.Buildings[0].Production.UnitType = "changed"
	clone := controller.ClonePlanProposal(plan)
	(*clone.ProductionOrders)[0].UnitType = "changed"
	if m.castle("faction-a").Production.UnitType != "vanguard" || (*plan.ProductionOrders)[0].UnitType != "vanguard" {
		t.Fatal("production alias")
	}
	before := m.State()
	if err := m.applyPlans(map[string]controller.PlanProposal{"faction-a": {Name: "unchanged", SpendOrder: []string{"upgrades"}}}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, m.State()) {
		t.Fatal("omitted orders changed production")
	}
}

func TestContinuousScriptedPlansConfigureNewBuildings(t *testing.T) {
	m := continuousMatch(t)
	if _, err := m.Advance(); err != nil {
		t.Fatal(err)
	}
	m.factionState("faction-a").Gold = 1000
	sites := m.legalConstructionSites("faction-a")
	b, reason := m.buildStructureAt("faction-a", BuildingArcheryRange, Position{X: sites[0].X, Y: sites[0].Y}, 0)
	if reason != "" || b.Production == nil || b.Production.Enabled {
		t.Fatal("new source must start off", reason)
	}
	plan := m.scriptedProductionPlan("faction-a", controller.PlanProposal{Name: "baseline"})
	if len(*plan.ProductionOrders) != 0 {
		t.Fatal("pending source configured")
	}
	m.state.Tick++
	plan = m.scriptedProductionPlan("faction-a", controller.PlanProposal{Name: "baseline"})
	if len(*plan.ProductionOrders) != 1 || (*plan.ProductionOrders)[0].UnitType != "ranger" {
		t.Fatal("new source not configured")
	}
	if err := m.applyPlans(map[string]controller.PlanProposal{"faction-a": plan}); err != nil {
		t.Fatal(err)
	}
	m.runContinuousProduction()
	if len(m.recruitmentSource("faction-a", b.ID).TrainingQueue) != 1 {
		t.Fatal("parallel source not training")
	}
}

func TestScriptedFallbackPreservesExplicitOffWithoutSelection(t *testing.T) {
	m := continuousMatch(t)
	m.applyBuildingProduction(productionPlan("", false))
	plan := m.scriptedProductionPlan("faction-a", controller.PlanProposal{Name: "fallback"})
	if len(*plan.ProductionOrders) != 0 {
		t.Fatal("fallback enabled an explicitly disabled source")
	}
}
