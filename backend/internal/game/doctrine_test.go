// Checks tick-zero opening behavior, upgrade ordering and budgets, frozen views, and
// legacy fallback behavior.

package game

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
)

func doctrineMatch(t *testing.T) *Match {
	t.Helper()
	scenario := testScenario()
	scenario.SnapshotVersion = DoctrineSnapshotVersion
	scenario.Ruleset.DesignBudget = 0
	scenario.Ruleset.StartingGold = 180
	for i := range scenario.Factions {
		scenario.Factions[i].Doctrine = "Protect income"
		scenario.Factions[i].Blueprint = Blueprint{}
	}
	m, err := NewMatch("doctrine", scenario, 100)
	if err != nil {
		t.Fatal(err)
	}
	return m
}
func noPurchases() controller.PlanProposal {
	orders := []controller.RecruitmentOrder{}
	return controller.PlanProposal{Name: "save", RecruitmentOrders: &orders, SpendOrder: []string{"recruitment"}}
}
func TestOpeningPreservesGoldAndFrozenViews(t *testing.T) {
	m := doctrineMatch(t)
	ids := m.FactionIDs()
	if len(m.State().Units) != 0 || !m.DecisionWindowDue() || m.DecisionTargetTick() != 0 {
		t.Fatal("opening initialization")
	}
	for _, id := range ids {
		snapshot, err := m.DecisionSnapshot(id)
		if err != nil || !snapshot.Opening || snapshot.ProjectedIncome != 0 || snapshot.Gold != 180 || snapshot.Doctrine != "Protect income" || len(snapshot.LegalSites) != 0 {
			t.Fatalf("snapshot %+v %v", snapshot, err)
		}
	}
	tr, err := m.AdvanceWithPlans(map[string]controller.PlanProposal{ids[0]: noPurchases(), ids[1]: noPurchases()})
	if err != nil {
		t.Fatal(err)
	}
	if tr.Tick != 0 || len(tr.State.Units) != 0 || !tr.State.OpeningComplete || tr.State.Factions[0].Gold != 180 || tr.State.Factions[0].GoldEarned != 0 {
		t.Fatalf("opening: %+v", tr.State)
	}
	if _, err = m.AdvanceWithPlans(map[string]controller.PlanProposal{ids[0]: noPurchases()}); err == nil {
		t.Fatal("opening replay accepted")
	}
	if _, err = m.Advance(); err != nil {
		t.Fatal(err)
	}
	if m.State().Tick != 1 || m.State().Factions[0].Gold != 190 {
		t.Fatal("first income")
	}
}
func TestOpeningUpgradeEffectsAndRepeatPurchases(t *testing.T) {
	m := doctrineMatch(t)
	ids := m.FactionIDs()
	id := ids[0]
	orders := []controller.RecruitmentOrder{{UnitType: "ranger", Count: 1}}
	plan := controller.PlanProposal{Name: "upgrade", RecruitmentOrders: &orders, UpgradeOrders: []string{"arsenal", "industry"}, SpendOrder: []string{"recruitment", "upgrades"}}
	tr, err := m.AdvanceWithPlans(map[string]controller.PlanProposal{id: plan, ids[1]: noPurchases()})
	if err != nil {
		t.Fatal(err)
	}
	if tr.State.Factions[0].Gold != 110 || tr.State.Units[0].Attack != 13 || tr.State.Units[0].ReadyAtTick != 1 {
		t.Fatalf("purchases %+v", tr.State)
	}
	snapshot, _ := m.DecisionSnapshot(id)
	snapshot.OwnedUpgradeIDs[0] = "mutated"
	if m.State().Factions[0].OwnedUpgradeIDs[0] != "arsenal" {
		t.Fatal("snapshot aliases state")
	}
	stateCopy := m.State()
	stateCopy.Factions[0].OwnedUpgradeIDs[0] = "mutated"
	if m.State().Factions[0].OwnedUpgradeIDs[0] != "arsenal" {
		t.Fatal("state aliases")
	}
	before := m.factionState(id).Gold
	if events := m.runUpgrades(id, plan); len(events) != 0 || m.factionState(id).Gold != before || m.State().Units[0].Attack != 13 {
		t.Fatal("duplicate upgrade charged")
	}
	site := m.scenario.Map.EconomySites[0][0]
	building, reason := m.buildEconomyBuildingAt(id, site, 0)
	if reason != "" || building.Income != 12 {
		t.Fatalf("future economy upgrade: %+v %s", building, reason)
	}
	m.factionState(id).Gold = 100
	m.runUpgrades(id, controller.PlanProposal{UpgradeOrders: []string{"plating"}})
	if m.State().Units[0].Defense != 4 {
		t.Fatal("existing unit defense")
	}
	events := m.runRecruitment(id, controller.PlanProposal{RecruitmentOrders: &orders})
	if len(events) == 0 || len(m.State().Units) != 2 || m.State().Units[1].Attack != 13 || m.State().Units[1].Defense != 4 {
		t.Fatal("future unit bonuses")
	}
}
func TestUpgradeBudgetOrderAndExistingEconomy(t *testing.T) {
	m := doctrineMatch(t)
	id := m.FactionIDs()[0]
	m.AdvanceWithPlans(map[string]controller.PlanProposal{id: noPurchases(), m.FactionIDs()[1]: noPurchases()})
	m.buildEconomyBuildingAt(id, m.scenario.Map.EconomySites[0][0], 0)
	m.factionState(id).Gold = 35
	plan := controller.PlanProposal{UpgradeOrders: []string{"industry", "arsenal"}, ReserveGold: 10}
	events := m.runUpgrades(id, plan)
	if len(events) != 2 || m.factionState(id).Gold != 15 || len(m.factionState(id).OwnedUpgradeIDs) != 1 {
		t.Fatal("reserve/order")
	}
	if m.incomeFor(id, 10) != 22 {
		t.Fatal("existing building income")
	}
}
func TestOpeningRejectsInvalidPlansWithoutSpending(t *testing.T) {
	for _, mutate := range []func(*controller.PlanProposal){
		func(p *controller.PlanProposal) { p.UpgradeOrders = []string{"unknown"} },
		func(p *controller.PlanProposal) { p.UpgradeOrders = []string{"arsenal", "arsenal"} },
		func(p *controller.PlanProposal) { p.SpendOrder = []string{"construction"} },
		func(p *controller.PlanProposal) { p.ConstructionPriorities = []controller.Coordinate{{X: 1, Y: 1}} },
		func(p *controller.PlanProposal) { p.RecruitmentOrders = nil },
		func(p *controller.PlanProposal) {
			o := []controller.RecruitmentOrder{{UnitType: "ranger", Count: 7}}
			p.RecruitmentOrders = &o
		},
	} {
		m := doctrineMatch(t)
		before := m.State()
		plan := noPurchases()
		mutate(&plan)
		if _, err := m.AdvanceWithPlans(map[string]controller.PlanProposal{m.FactionIDs()[0]: plan}); err == nil {
			t.Fatal("invalid plan accepted")
		}
		if !reflect.DeepEqual(before, m.State()) {
			t.Fatal("rejected opening changed state")
		}
	}
}
func TestOpeningFallbackAndLegacySnapshot(t *testing.T) {
	a, b := doctrineMatch(t), doctrineMatch(t)
	ta, err := a.Advance()
	if err != nil {
		t.Fatal(err)
	}
	tb, err := b.Advance()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ta, tb) || ta.Tick != 0 || len(ta.State.Units) != 12 {
		t.Fatal("fallback is not deterministic")
	}
	first := a.OpeningFallback(a.FactionIDs()[0])
	if first.RecruitmentOrders == nil {
		t.Fatal("explicit orders required")
	}
	legacy := testScenario()
	payload, _ := json.Marshal(legacy)
	var restored Scenario
	if err = json.Unmarshal(payload, &restored); err != nil {
		t.Fatal(err)
	}
	old, err := NewMatch("legacy", restored, 20)
	if err != nil {
		t.Fatal(err)
	}
	if old.OpeningDue() || len(old.State().Units) == 0 {
		t.Fatal("legacy initialization changed")
	}
	tr, err := old.Advance()
	if err != nil || tr.Tick != 1 {
		t.Fatal("legacy first tick changed")
	}
}
