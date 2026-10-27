// Checks per-type battle statistics: who is credited for damage and destroying blows,
// what each type cost, and that the per-type figures add up to the faction totals.

package game

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUnitKillCreditsAttackerTypeAndNamesKiller(t *testing.T) {
	m := expansionMatch(t)
	m.state.Units = []Unit{soldier("attacker", "faction-a", Position{X: 2, Y: 2}), soldier("victim", "faction-b", Position{X: 3, Y: 2})}
	m.state.Units[1].Health = 1
	attacker := &m.state.Units[0]
	events, _ := m.attackTarget(attacker, m.targetInRange(*attacker), nil)
	if len(events) != 2 || events[1].Type != "unit_destroyed" {
		t.Fatalf("events = %+v", events)
	}
	want := map[string]any{"unitId": "victim", "unitType": "vanguard", "factionId": "faction-b", "killerId": "attacker", "killerType": "vanguard", "killerFactionId": "faction-a"}
	for key, value := range want {
		if events[1].Data[key] != value {
			t.Errorf("unit_destroyed %s = %v, want %v", key, events[1].Data[key], value)
		}
	}
	credit, charge := m.factions["faction-a"].stats.units["vanguard"], m.factions["faction-b"].stats.units["vanguard"]
	if credit.Kills != 1 || credit.DamageDealt != 1 || charge.Lost != 1 || charge.DamageTaken != 1 {
		t.Fatalf("credit = %+v, charge = %+v", credit, charge)
	}
}

func TestTowerKillsAndConstructionCountUnderBuildingType(t *testing.T) {
	m := expansionMatch(t)
	tower, reason := m.buildStructureAt("faction-a", BuildingTower, Position{X: 2, Y: 2}, 0)
	if reason != "" {
		t.Fatal(reason)
	}
	enemy := soldier("enemy", "faction-b", Position{X: 3, Y: 2})
	enemy.Health, enemy.Defense = 5, 0
	m.state.Units = []Unit{enemy}
	m.state.Tick++
	events := m.runTowers(nil)
	if len(events) != 2 || events[1].Data["killerId"] != tower.ID || events[1].Data["killerType"] != BuildingTower {
		t.Fatalf("events = %+v", events)
	}
	built := m.factions["faction-a"].stats.buildings[BuildingTower]
	if built.Constructed != 1 || built.GoldSpent != 80 || built.Kills != 1 || built.DamageDealt != 5 {
		t.Fatalf("tower stats = %+v", built)
	}
	if _, credited := m.factions["faction-a"].stats.units[""]; credited {
		t.Fatal("tower fire credited to a unit type")
	}
}

func TestBuildingAndCastleDestructionCreditAttacker(t *testing.T) {
	m := expansionMatch(t)
	m.state.Buildings = append(m.state.Buildings, Building{ID: "watch", FactionID: "faction-b", Type: BuildingWatchtower, Position: Position{X: 3, Y: 2}, Health: 1, MaxHealth: 50, Cost: 30})
	m.state.Units = []Unit{soldier("raider", "faction-a", Position{X: 2, Y: 2})}
	raider := &m.state.Units[0]
	events, _ := m.attackTarget(raider, m.targetInRange(*raider), nil)
	if events[1].Type != "building_destroyed" || events[1].Data["killerType"] != "vanguard" || events[1].Data["buildingType"] != BuildingWatchtower {
		t.Fatalf("events = %+v", events)
	}
	castle := m.castle("faction-b")
	castle.Health = 1
	raider.Position = Position{X: castle.Position.X - 1, Y: castle.Position.Y}
	events, winner := m.attackTarget(raider, m.targetInRange(*raider), nil)
	if winner == nil || events[1].Type != "castle_destroyed" || events[1].Data["killerId"] != "raider" || events[1].Data["destroyedByFactionId"] != "faction-a" {
		t.Fatalf("events = %+v, winner = %v", events, winner)
	}
	if got := m.factions["faction-a"].stats.units["vanguard"].BuildingsDestroyed; got != 2 {
		t.Fatalf("buildings destroyed = %d, want watchtower and castle", got)
	}
	lost := m.factions["faction-b"].stats.buildings
	if lost[BuildingWatchtower].Lost != 1 || lost[BuildingCastle].Lost != 1 || lost[BuildingCastle].DamageTaken != 1 {
		t.Fatalf("defender buildings = %+v %+v", *lost[BuildingWatchtower], *lost[BuildingCastle])
	}
}

func TestSupportUnitsRecordHealthRestored(t *testing.T) {
	m := expansionMatch(t)
	patient := soldier("patient", "faction-a", Position{X: 2, Y: 3})
	patient.Health = 30
	m.state.Units = []Unit{healer(Position{X: 2, Y: 2}), patient}
	if events := m.runHealer(&m.state.Units[0]); len(events) != 1 {
		t.Fatalf("healer events = %+v", events)
	}
	engineer := Unit{ID: "engineer", FactionID: "faction-a", Type: "engineer", Class: "support", Position: Position{X: 2, Y: 6}, Health: 30, MaxHealth: 30, RepairAmount: 5, RepairCost: 2, RepairCharges: 1, Movement: 1}
	m.state.Units = []Unit{engineer}
	m.state.Buildings = append(m.state.Buildings, Building{ID: "barracks", FactionID: "faction-a", Type: BuildingBarracks, Position: Position{X: 3, Y: 6}, Health: 50, MaxHealth: 100})
	if events := m.runEngineer(&m.state.Units[0]); len(events) != 1 || events[0].Type != "unit_repaired" {
		t.Fatalf("engineer events = %+v", events)
	}
	stats := m.factions["faction-a"].stats.units
	if stats["herbalist"].HealthRestored != 3 || stats["engineer"].HealthRestored != 5 {
		t.Fatalf("health restored: herbalist %+v engineer %+v", stats["herbalist"], stats["engineer"])
	}
}

func TestUpgradePurchasesRecordTheirTick(t *testing.T) {
	m := expansionMatch(t)
	m.state.Tick = 7
	m.purchaseUpgrade("faction-a", Upgrade{ID: "arsenal", Cost: 20, AttackBonus: 2})
	got := m.factions["faction-a"].stats.upgradeList()
	if len(got) != 1 || got[0] != (UpgradePurchase{UpgradeID: "arsenal", Tick: 7}) {
		t.Fatalf("upgrades = %+v", got)
	}
}

func TestTypeStatisticsAddUpToFactionTotals(t *testing.T) {
	for name, scenario := range map[string]Scenario{"starting units": testScenario(), "scoped upgrades": scopedUpgradeScenario()} {
		t.Run(name, func(t *testing.T) {
			match, err := NewMatch("totals", scenario, 1000)
			if err != nil {
				t.Fatal(err)
			}
			var final Transition
			for !match.Finished() {
				if final, err = match.Advance(); err != nil {
					t.Fatal(err)
				}
			}
			requireTypeTotals(t, final)
		})
	}
}

// typeTotals is what a faction's per-type lists add up to; kills includes tower kills.
type typeTotals struct {
	started, recruited, lost, kills, constructed int
	dealt, taken                                 int64
}

func sumTypes(faction FactionStats) typeTotals {
	var sum typeTotals
	for _, unit := range faction.UnitTypes {
		sum.started, sum.recruited, sum.lost, sum.kills = sum.started+unit.Started, sum.recruited+unit.Recruited, sum.lost+unit.Lost, sum.kills+unit.Kills
		sum.dealt, sum.taken = sum.dealt+unit.DamageDealt, sum.taken+unit.DamageTaken
	}
	for _, building := range faction.BuildingTypes {
		sum.constructed, sum.kills = sum.constructed+building.Constructed, sum.kills+building.Kills
		sum.dealt, sum.taken = sum.dealt+building.DamageDealt, sum.taken+building.DamageTaken
	}
	return sum
}

func requireTypeTotals(t *testing.T, final Transition) {
	t.Helper()
	for _, faction := range final.Result.Factions {
		want := typeTotals{started: faction.UnitsStarted, recruited: faction.UnitsRecruited, lost: faction.UnitsLost, kills: enemyUnitsLost(*final.Result, faction.FactionID), constructed: faction.BuildingsConstructed, dealt: faction.DamageDealt, taken: faction.DamageTaken}
		if got := sumTypes(faction); got != want {
			t.Errorf("%s per-type sums %+v, faction totals %+v", faction.FactionID, got, want)
		}
		if len(faction.Upgrades) != len(ownedUpgrades(final.State, faction.FactionID)) {
			t.Errorf("%s upgrades %+v", faction.FactionID, faction.Upgrades)
		}
	}
}

func enemyUnitsLost(result Result, factionID string) int {
	for _, faction := range result.Factions {
		if faction.FactionID != factionID {
			return faction.UnitsLost
		}
	}
	return 0
}

func ownedUpgrades(state State, factionID string) []string {
	for _, faction := range state.Factions {
		if faction.FactionID == factionID {
			return faction.OwnedUpgradeIDs
		}
	}
	return nil
}

func TestResultOmitsEmptyListsAndClonesIndependently(t *testing.T) {
	m := expansionMatch(t)
	result := m.buildResult(nil, "tick_limit")
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"unitTypes":[`) || strings.Contains(string(data), `"buildingTypes"`) || strings.Contains(string(data), `"upgrades"`) {
		t.Fatalf("only non-empty lists may serialize: %s", data)
	}
	result.Factions[0].UnitTypes = []UnitTypeStats{{UnitType: "vanguard", Lost: 1}}
	result.Factions[0].BuildingTypes = []BuildingTypeStats{{BuildingType: BuildingTower}}
	result.Factions[0].Upgrades = []UpgradePurchase{{UpgradeID: "arsenal"}}
	clone := CloneResult(*result)
	clone.Factions[0].UnitTypes[0].Lost = 9
	clone.Factions[0].BuildingTypes[0].Lost = 9
	clone.Factions[0].Upgrades[0].Tick = 9
	if result.Factions[0].UnitTypes[0].Lost != 1 || result.Factions[0].BuildingTypes[0].Lost != 0 || result.Factions[0].Upgrades[0].Tick != 0 {
		t.Fatal("cloned result shares per-type lists")
	}
}
