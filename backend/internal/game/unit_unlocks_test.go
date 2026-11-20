package game

import (
	"errors"
	"slices"
	"testing"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/game/controller"
)

// lockedStrikerMatch locks the castle's Striker behind a 50-gold Gunpowder upgrade.
func lockedStrikerMatch(t *testing.T, continuous bool) *Match {
	t.Helper()
	s := specializedMatch(t).Scenario()
	s.Ruleset.ContinuousProduction = continuous
	s.Ruleset.Upgrades = append(s.Ruleset.Upgrades, Upgrade{ID: "gunpowder", Cost: 50})
	for i := range s.Ruleset.Units {
		if s.Ruleset.Units[i].ID == "striker" {
			s.Ruleset.Units[i].RequiresUpgradeID = "gunpowder"
		}
	}
	m, err := NewMatch("locked", s, 100)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func unlockPlan(unit string, upgrades ...string) controller.PlanProposal {
	plan := productionPlan(unit, true)
	plan.UpgradeOrders = upgrades
	return plan
}

func TestRulesetRejectsUnknownUnitUnlock(t *testing.T) {
	r := lockedStrikerMatch(t, true).Scenario().Ruleset
	if err := ValidateRuleset(r); err != nil {
		t.Fatal(err)
	}
	for i := range r.Units {
		if r.Units[i].ID == "striker" {
			r.Units[i].RequiresUpgradeID = "missing"
		}
	}
	if err := ValidateRuleset(r); !errors.Is(err, ErrInvalidRuleset) {
		t.Fatalf("unknown unlock accepted: %v", err)
	}
}

func TestUnitUnlockValidationAcceptsOwnedOrOrderedUpgrade(t *testing.T) {
	units := lockedStrikerMatch(t, true).Scenario().Ruleset.Units
	recruitment := []controller.RecruitmentOrder{{UnitType: "striker", Count: 1}}
	for name, plan := range map[string]controller.PlanProposal{
		"production":  productionPlan("striker", false),
		"recruitment": {RecruitmentOrders: &recruitment},
		"priorities":  {RecruitmentPriorities: []string{"vanguard", "striker"}},
	} {
		if err := ValidateUnitUnlocks(units, nil, plan); !errors.Is(err, controller.ErrInvalidPlan) {
			t.Fatalf("%s: locked unit accepted: %v", name, err)
		}
		if err := ValidateUnitUnlocks(units, []string{"gunpowder"}, plan); err != nil {
			t.Fatalf("%s: owned unlock rejected: %v", name, err)
		}
		plan.UpgradeOrders = []string{"gunpowder"}
		if err := ValidateUnitUnlocks(units, nil, plan); err != nil {
			t.Fatalf("%s: ordered unlock rejected: %v", name, err)
		}
	}
	if err := ValidateUnitUnlocks(units, nil, productionPlan("", false)); err != nil {
		t.Fatalf("an off order without a selection names no unit: %v", err)
	}
}

func TestEngineRejectsLockedProductionPlan(t *testing.T) {
	m := lockedStrikerMatch(t, true)
	if err := m.applyPlans(map[string]controller.PlanProposal{"faction-a": productionPlan("striker", true)}); !errors.Is(err, controller.ErrInvalidPlan) {
		t.Fatalf("engine applied a locked selection: %v", err)
	}
}

func TestLockedProductionWaitsFreeUntilUnlocked(t *testing.T) {
	m := lockedStrikerMatch(t, true)
	plan := unlockPlan("striker", "gunpowder")
	m.factions["faction-a"].activePlan = plan
	m.applyBuildingProduction(plan)
	b := m.castle("faction-a")
	gold := m.factionState("faction-a").Gold
	if len(m.runContinuousProduction()) != 0 || !b.Production.WaitingForUpgrade || len(b.TrainingQueue) != 0 || m.factionState("faction-a").Gold != gold {
		t.Fatal("trained or charged a locked unit")
	}
	m.purchaseUpgrade("faction-a", UpgradesByID(m.scenario.Ruleset)["gunpowder"])
	if len(m.runContinuousProduction()) != 1 || b.Production.WaitingForUpgrade || b.TrainingQueue[0].UnitType != "striker" {
		t.Fatal("unlocking did not resume training")
	}
}

func TestUnlockAndSelectionInOneWindowTrainsImmediately(t *testing.T) {
	m := lockedStrikerMatch(t, true)
	if _, err := m.AdvanceWithPlans(map[string]controller.PlanProposal{"faction-a": unlockPlan("striker", "gunpowder")}); err != nil {
		t.Fatal(err)
	}
	b := m.castle("faction-a")
	if !slices.Contains(m.factionState("faction-a").OwnedUpgradeIDs, "gunpowder") || len(b.TrainingQueue) != 1 || b.TrainingQueue[0].UnitType != "striker" {
		t.Fatalf("upgrades must run before production starts: %+v", b.TrainingQueue)
	}
}

func TestSelectionChangeClearsUpgradeWait(t *testing.T) {
	m := lockedStrikerMatch(t, true)
	m.applyBuildingProduction(unlockPlan("striker", "gunpowder"))
	b := m.castle("faction-a")
	b.Production.WaitingForUpgrade = true
	m.applyBuildingProduction(productionPlan("vanguard", true))
	if b.Production.WaitingForUpgrade {
		t.Fatal("a new selection kept the previous lock status")
	}
}

func TestScriptedProductionSelectsOnlyUnlockedTypes(t *testing.T) {
	m := lockedStrikerMatch(t, true)
	preferred := controller.PlanProposal{Name: "baseline", RecruitmentPriorities: []string{"striker", "vanguard"}}
	if plan := m.scriptedProductionPlan("faction-a", preferred); (*plan.ProductionOrders)[0].UnitType != "vanguard" {
		t.Fatalf("scripted plan selected a locked type: %+v", *plan.ProductionOrders)
	}
	m.factionState("faction-a").OwnedUpgradeIDs = []string{"gunpowder"}
	if plan := m.scriptedProductionPlan("faction-a", preferred); (*plan.ProductionOrders)[0].UnitType != "striker" {
		t.Fatalf("scripted plan ignored an unlocked preference: %+v", *plan.ProductionOrders)
	}
	m.factionState("faction-a").OwnedUpgradeIDs = nil
	m.scenario.Ruleset.Buildings.CastleTrains = []string{"striker"}
	if plan := m.scriptedProductionPlan("faction-a", preferred); len(*plan.ProductionOrders) != 0 {
		t.Fatal("a source with only locked types must stay unconfigured")
	}
}

func TestScriptedProductionAdoptsUnlockedTypeOnConfiguredSource(t *testing.T) {
	m := lockedStrikerMatch(t, true)
	preferred := controller.PlanProposal{Name: "baseline", RecruitmentPriorities: []string{"striker", "vanguard"}}
	orders := func(setting controller.PlanProposal, owned []string, plan controller.PlanProposal) []controller.ProductionOrder {
		m.applyBuildingProduction(setting)
		m.factionState("faction-a").OwnedUpgradeIDs = owned
		return *m.scriptedProductionPlan("faction-a", plan).ProductionOrders
	}
	if got := orders(productionPlan("vanguard", true), nil, preferred); len(got) != 0 {
		t.Fatalf("configured source changed before the unlock: %+v", got)
	}
	if got := orders(productionPlan("vanguard", true), []string{"gunpowder"}, preferred); len(got) != 1 || got[0].UnitType != "striker" || !got[0].Enabled {
		t.Fatalf("configured source kept a classic type after the unlock: %+v", got)
	}
	for name, setting := range map[string]controller.PlanProposal{"disabled": productionPlan("vanguard", false), "late-game": productionPlan("striker", true)} {
		if got := orders(setting, []string{"gunpowder"}, preferred); len(got) != 0 {
			t.Fatalf("%s source changed: %+v", name, got)
		}
	}
	classicOnly := controller.PlanProposal{Name: "baseline", RecruitmentPriorities: []string{"vanguard"}}
	if got := orders(productionPlan("vanguard", true), []string{"gunpowder"}, classicOnly); len(got) != 0 {
		t.Fatalf("source switched to a late-game type the plan does not prefer: %+v", got)
	}
}

func TestLegacyRecruitmentSkipsLockedTypes(t *testing.T) {
	m := lockedStrikerMatch(t, false)
	for _, order := range *m.OpeningFallback("faction-a").RecruitmentOrders {
		if order.UnitType == "striker" {
			t.Fatal("opening fallback bought a locked type")
		}
	}
	event, bought, stop := m.attemptRecruitment("faction-a", m.castle("faction-a"), UnitTypesByID(m.scenario.Ruleset), []string{"striker"}, 0)
	if bought || stop || event.Data["reason"] != "unit_locked" {
		t.Fatalf("recruitment bought a locked type: %+v", event)
	}
	orders := []controller.RecruitmentOrder{{UnitType: "striker", Count: 1}, {UnitType: "vanguard", Count: 1}}
	plan := controller.PlanProposal{RecruitmentOrders: &orders}
	m.filterScriptedRecruitment("faction-a", &plan)
	if len(*plan.RecruitmentOrders) != 1 || (*plan.RecruitmentOrders)[0].UnitType != "vanguard" {
		t.Fatalf("scripted recruitment kept a locked type: %+v", *plan.RecruitmentOrders)
	}
}
