package game

import "testing"

func TestCastleFiresWhileTraining(t *testing.T) {
	m := specializedMatch(t)
	castle := m.castle("faction-a")
	queueUnits(t, m, castle.ID, "vanguard")
	m.state.Tick = 1
	enemy := soldier("enemy", "faction-b", Position{castle.Position.X + 3, castle.Position.Y})
	enemy.Defense = 0
	m.state.Units = []Unit{enemy}
	events := m.runTowers(nil)
	if len(events) != 1 || events[0].Type != "building_attacked" || events[0].Data["damage"] != int64(15) {
		t.Fatal("castle did not fire at range three", events)
	}
	if castle.AttackTargetID != enemy.ID || m.state.Units[0].Health != enemy.Health-15 {
		t.Fatal("castle attack state missing")
	}
	m.runTraining()
	if len(m.state.Units) != 2 || m.state.Units[1].Type != "vanguard" || len(castle.TrainingQueue) != 0 {
		t.Fatal("castle attack prevented training")
	}
}

func TestCastleRespectsTargetEligibility(t *testing.T) {
	for _, target := range []struct {
		name, faction string
		distance      int
		health        int64
	}{
		{"friendly", "faction-a", 1, 50},
		{"outside range", "faction-b", 4, 50},
		{"destroyed", "faction-b", 1, 0},
	} {
		t.Run(target.name, func(t *testing.T) {
			m := specializedMatch(t)
			castle := m.castle("faction-a")
			u := soldier("target", target.faction, Position{castle.Position.X + target.distance, castle.Position.Y})
			u.Health = target.health
			m.state.Units = []Unit{u}
			if events := m.runTowers(nil); len(events) != 0 {
				t.Fatal("castle attacked ineligible target", events)
			}
		})
	}
}

func TestDestroyedCastleStopsFiringAndLosesQueue(t *testing.T) {
	m := specializedMatch(t)
	castle := m.castle("faction-a")
	queueUnits(t, m, castle.ID, "vanguard")
	paid := m.factionState("faction-a").Gold
	castle.Health = 0
	m.state.Units = []Unit{soldier("enemy", "faction-b", Neighbors(castle.Position)[0])}
	if events := m.runTowers(nil); len(events) != 0 {
		t.Fatal("destroyed castle fired", events)
	}
	m.runTraining()
	if len(castle.TrainingQueue) != 0 || len(m.recruitmentSources("faction-a")) != 0 || m.factionState("faction-a").Gold != paid {
		t.Fatal("destroyed castle retained production or refunded gold")
	}
}

func TestHistoricalSpecializedOpeningRetainsBarracks(t *testing.T) {
	scenario := specializedMatch(t).Scenario()
	scenario.Ruleset.Buildings.CastleTrains = nil
	scenario.Ruleset.Buildings.CastleAttack = 0
	scenario.Ruleset.Buildings.CastleRange = 0
	m, err := NewMatch("historical-specialized", scenario, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.state.Buildings) != 4 {
		t.Fatal("historical starting buildings changed")
	}
	for _, id := range m.FactionIDs() {
		castle := m.castle(id)
		source := m.defaultTrainingSource(id, "vanguard")
		if len(castle.Trains) != 0 || castle.Attack != 0 || source.Type != BuildingBarracks || HexDistance(source.Position, castle.Position) < 2 {
			t.Fatal("historical castle/Barracks capabilities changed")
		}
	}
}

func TestCastleAttackValidation(t *testing.T) {
	for _, attack := range []struct{ power, distance int }{{-1, 3}, {15, -1}, {15, 0}, {0, 3}} {
		rules := specializedMatch(t).Scenario().Ruleset
		rules.Buildings.CastleAttack = int64(attack.power)
		rules.Buildings.CastleRange = attack.distance
		if ValidateRuleset(rules) == nil {
			t.Fatal("invalid castle attack accepted", attack)
		}
	}
}
