package game

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
)

func levelUpgrades() []Upgrade {
	return []Upgrade{
		{ID: "arsenal", Track: "arsenal", Level: 1, Cost: 20, AttackBonus: 2},
		{ID: "arsenal_2", Track: "arsenal", Level: 2, RequiresUpgradeID: "arsenal", Cost: 50, AttackBonus: 2},
		{ID: "arsenal_3", Track: "arsenal", Level: 3, RequiresUpgradeID: "arsenal_2", Cost: 100, AttackBonus: 2},
	}
}

func TestUpgradeLevelValidation(t *testing.T) {
	if err := validateUpgradeLevels(levelUpgrades()); err != nil {
		t.Fatal(err)
	}
	if err := validateUpgradeLevels([]Upgrade{{ID: "legacy", Cost: 20}}); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func([]Upgrade){
		func(u []Upgrade) { u[0].Track = " " },
		func(u []Upgrade) { u[0].Level = 0 },
		func(u []Upgrade) { u[2].Level = 4 },
		func(u []Upgrade) { u[2].Level = 2 },
		func(u []Upgrade) { u[0].RequiresUpgradeID = "arsenal_3" },
		func(u []Upgrade) { u[1].RequiresUpgradeID = "missing" },
		func(u []Upgrade) { u[1].Track = "plating" },
		func(u []Upgrade) { u[2].RequiresUpgradeID = "arsenal" },
		func(u []Upgrade) { u[1].Cost = 20 },
	} {
		u := levelUpgrades()
		mutate(u)
		if err := validateUpgradeLevels(u); err == nil {
			t.Fatalf("accepted %+v", u)
		}
	}
	u := levelUpgrades()
	for _, tc := range []struct {
		owned, orders []string
		valid         bool
	}{
		{nil, []string{"arsenal", "arsenal_2", "arsenal_3"}, true},
		{[]string{"arsenal"}, []string{"arsenal_2"}, true},
		{[]string{"arsenal", "arsenal_2"}, []string{"arsenal", "arsenal_2", "arsenal_3"}, true},
		{nil, []string{"arsenal_2"}, false},
		{nil, []string{"arsenal_2", "arsenal"}, false},
		{nil, []string{"arsenal", "arsenal_3"}, false},
		{nil, []string{"arsenal_4"}, false},
	} {
		if err := ValidateUpgradeOrder(u, tc.owned, tc.orders); (err == nil) != tc.valid {
			t.Fatalf("%+v: %v", tc, err)
		}
	}
	definitions := UpgradesByID(Ruleset{Upgrades: u})
	if cost, err := validateUpgradeSelections(definitions, []string{"arsenal_3", "arsenal", "arsenal_2"}); err != nil || cost != 170 {
		t.Fatalf("blueprint cost %d: %v", cost, err)
	}
	if _, err := validateUpgradeSelections(definitions, []string{"arsenal_2"}); err == nil {
		t.Fatal("blueprint skipped prerequisite")
	}
}

func TestUpgradeLevelsPartialPurchase(t *testing.T) {
	m := doctrineMatch(t)
	id := m.FactionIDs()[0]
	m.scenario.Ruleset.Upgrades = levelUpgrades()
	m.factionState(id).Gold = 80
	plan := noPurchases()
	plan.UpgradeOrders = []string{"arsenal", "arsenal_2", "arsenal_3"}
	plan.SpendOrder = []string{"upgrades"}
	plan.ReserveGold = 10
	tr, err := m.AdvanceWithPlans(map[string]controller.PlanProposal{id: plan, m.FactionIDs()[1]: noPurchases()})
	if err != nil {
		t.Fatal(err)
	}
	if tr.State.Factions[0].Gold != 10 || len(tr.State.Factions[0].OwnedUpgradeIDs) != 2 {
		t.Fatalf("partial purchase %+v", tr.State.Factions[0])
	}
	// Repeated production plan resumes at III without paying for I/II again.
	m.factionState(id).Gold = 110
	events := m.runUpgrades(id, plan)
	if len(events) != 1 || m.factionState(id).Gold != 10 || m.factions[id].attackBonus != 6 || events[0].Data["level"] != 3 {
		t.Fatalf("resume: %+v", events)
	}
	if len(m.runUpgrades(id, plan)) != 0 || m.factionState(id).Gold != 10 {
		t.Fatal("max-level repeat charged")
	}
}

func TestUpgradeLevelsRejectMissingPrerequisitesBeforeMutation(t *testing.T) {
	// Missing prerequisites cannot be applied even by an internal spending call.
	fresh := doctrineMatch(t)
	id := fresh.FactionIDs()[0]
	fresh.scenario.Ruleset.Upgrades = levelUpgrades()
	skipped := fresh.runUpgrades(id, controller.PlanProposal{UpgradeOrders: []string{"arsenal_3"}})
	if len(skipped) != 1 || skipped[0].Data["reason"] != "prerequisite_required" || fresh.factions[id].attackBonus != 0 {
		t.Fatal("prerequisite bypass")
	}
	// An invalid opening is rejected before mutating either faction.
	before := fresh.State()
	bad := noPurchases()
	bad.UpgradeOrders = []string{"arsenal_3"}
	bad.SpendOrder = []string{"upgrades"}
	if _, err := fresh.AdvanceWithPlans(map[string]controller.PlanProposal{id: bad}); err == nil || !reflect.DeepEqual(before, fresh.State()) {
		t.Fatal("invalid opening mutated state")
	}
}

func TestUpgradeLevelsSnapshotReplay(t *testing.T) {
	// Snapshot serialization retains prerequisites; replay produces identical transitions.
	original := doctrineMatch(t)
	id := original.FactionIDs()[0]
	plan := noPurchases()
	plan.UpgradeOrders = []string{"arsenal", "arsenal_2", "arsenal_3"}
	plan.SpendOrder = []string{"upgrades"}
	plan.ReserveGold = 10
	original.scenario.Ruleset.Upgrades = levelUpgrades()
	payload, err := json.Marshal(original.scenario)
	if err != nil {
		t.Fatal(err)
	}
	var restored Scenario
	if err = json.Unmarshal(payload, &restored); err != nil {
		t.Fatal(err)
	}
	a, err := NewMatch("replay", original.scenario, 100)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewMatch("replay", restored, 100)
	if err != nil {
		t.Fatal(err)
	}
	plans := map[string]controller.PlanProposal{id: plan, original.FactionIDs()[1]: noPurchases()}
	ta, ea := a.AdvanceWithPlans(plans)
	tb, eb := b.AdvanceWithPlans(plans)
	if ea != nil || eb != nil || !reflect.DeepEqual(ta, tb) {
		t.Fatalf("replay mismatch %v %v", ea, eb)
	}
}

func TestThreeLevelEffectsOnExistingAndFutureEntities(t *testing.T) {
	m := doctrineMatch(t)
	id := m.FactionIDs()[0]
	m.scenario.Ruleset.Upgrades = allGeneralUpgradeLevels()
	orders := []string{}
	for _, upgrade := range m.scenario.Ruleset.Upgrades {
		orders = append(orders, upgrade.ID)
	}
	m.factionState(id).Gold = 2000
	ranger := UnitTypesByID(m.scenario.Ruleset)["ranger"]
	healer := UnitType{ID: "herbalist", Health: 25, Attack: 0, Defense: 1, HealAmount: 3}
	m.state.Units = append(m.state.Units, m.factions[id].newUnit(ranger, Position{X: 2, Y: 4}, 0), m.factions[id].newUnit(healer, Position{X: 2, Y: 5}, 0))
	first, reason := m.buildEconomyBuildingAt(id, m.scenario.Map.EconomySites[0][0], 0)
	if reason != "" {
		t.Fatal(reason)
	}
	castle := *m.castle(id)
	m.runUpgrades(id, controller.PlanProposal{UpgradeOrders: orders})
	if m.state.Units[0].Attack != ranger.Attack+6 || m.state.Units[0].Defense != ranger.Defense+6 || m.state.Units[1].Attack != 0 || m.state.Units[1].HealAmount != 3 || m.state.Units[1].Defense != 7 {
		t.Fatalf("unit effects %+v", m.state.Units)
	}
	future := m.factions[id].newUnit(ranger, Position{X: 3, Y: 4}, 0)
	if future.Attack != ranger.Attack+6 || future.Defense != ranger.Defense+6 {
		t.Fatal("future unit effects")
	}
	for _, b := range m.state.Buildings {
		if b.ID == first.ID && b.Income != 16 {
			t.Fatal("existing income")
		}
	}
	second, reason := m.buildEconomyBuildingAt(id, m.scenario.Map.EconomySites[0][1], 0)
	if reason != "" || second.Income != 16 {
		t.Fatalf("future income %+v %s", second, reason)
	}
	if *m.castle(id) != castle || m.incomeFor(id, castle.Income) != 42 {
		t.Fatal("castle changed or income incorrect")
	}
}

func allGeneralUpgradeLevels() []Upgrade {
	var upgrades []Upgrade
	for _, track := range []string{"arsenal", "plating", "industry"} {
		for _, u := range levelUpgrades() {
			u.ID = strings.ReplaceAll(u.ID, "arsenal", track)
			u.RequiresUpgradeID = strings.ReplaceAll(u.RequiresUpgradeID, "arsenal", track)
			u.Track = track
			u.AttackBonus = 0
			switch track {
			case "arsenal":
				u.AttackBonus = 2
			case "plating":
				u.DefenseBonus = 2
			case "industry":
				u.EconomyIncomeBonus = 2
			}
			upgrades = append(upgrades, u)
		}
	}
	return upgrades
}
