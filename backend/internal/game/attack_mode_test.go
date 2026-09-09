package game

import (
	"reflect"
	"testing"
)

func aimedMatch(t *testing.T) (*Match, Position) {
	t.Helper()
	m := expansionMatch(t)
	cat := soldier("cat", "faction-a", Position{2, 5})
	cat.AttackMode, cat.Speed, cat.Range, cat.Attack = "position", 2, 4, 10
	target := soldier("target", "faction-b", Position{5, 5})
	target.Speed = 8
	m.state.Units = []Unit{cat, target}
	m.captureAttackAims()
	return m, target.Position
}

func TestPositionAttackOccupancy(t *testing.T) {
	for _, tc := range []struct {
		name            string
		moved           bool
		occupantFaction string
		dead            bool
		targetID        string
	}{
		{name: "stationary", targetID: "target"},
		{name: "empty", moved: true},
		{name: "friendly", moved: true, occupantFaction: "faction-a"},
		{name: "replacement", moved: true, occupantFaction: "faction-b", targetID: "occupant"},
		{name: "dead", moved: true, occupantFaction: "faction-b", dead: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, aim := aimedMatch(t)
			originalHealth := m.state.Units[1].Health
			if tc.moved {
				m.state.Units[1].Position = Position{6, 5}
			}
			if tc.occupantFaction != "" {
				occupant := soldier("occupant", tc.occupantFaction, aim)
				if tc.dead {
					occupant.Health = 0
				}
				m.state.Units = append(m.state.Units, occupant)
			}
			assertPositionAttack(t, m, aim, tc.targetID)
			if tc.moved && m.state.Units[1].Health != originalHealth {
				t.Fatal("original target damaged after moving")
			}
		})
	}
}

func assertPositionAttack(t *testing.T, m *Match, aim Position, targetID string) {
	t.Helper()
	events, _ := m.runUnit(&m.state.Units[0], nil)
	position := m.state.Units[0].AttackTargetPosition
	if len(events) != 1 || position == nil || *position != aim {
		t.Fatalf("shot: %+v", events)
	}
	if targetID == "" {
		if events[0].Type != "unit_attack_missed" || m.state.Units[0].AttackOutcome != "miss" {
			t.Fatal("expected miss")
		}
		return
	}
	if events[0].Data["outcome"] != "hit" || events[0].Data["damage"].(int64) <= 0 {
		t.Fatal("expected damage")
	}
	if events[0].Data["targetId"] != targetID {
		t.Fatal("wrong occupant")
	}
}

func TestPositionAttackTurnOrderAndLegacyTracking(t *testing.T) {
	for _, mode := range []string{"position", "", "tracking"} {
		m, _ := aimedMatch(t)
		m.state.Units[0].AttackMode = mode
		before := m.state.Units[1].Health
		events, _ := m.runUnits(nil)
		if m.state.Units[1].MovedFrom == nil {
			t.Fatal("faster target did not move")
		}
		if mode == "position" {
			if m.state.Units[1].Health != before || events[len(events)-1].Type != "unit_attack_missed" {
				t.Fatalf("did not evade: %+v", events)
			}
		} else if m.state.Units[1].Health >= before {
			t.Fatal("tracking attack did not hit")
		}
	}
}

func TestPositionAttackCancellationAndNoLateAim(t *testing.T) {
	t.Run("retreat", func(t *testing.T) {
		m, _ := aimedMatch(t)
		m.state.Units[0].MinimumRange = 2
		m.state.Units = append(m.state.Units, soldier("threat", "faction-b", Position{3, 5}))
		events, _ := m.runUnit(&m.state.Units[0], nil)
		if m.state.Units[0].AttackTargetPosition != nil || len(events) == 0 || events[0].Type != "unit_moved" {
			t.Fatalf("retreat fired: %+v", events)
		}
	})
	for _, tc := range []struct {
		name    string
		prepare func(*Match)
	}{
		{"dead", func(m *Match) { m.state.Units[0].Health = 0 }},
		{"unready", func(m *Match) { m.state.Units[0].ReadyAtTick = m.state.Tick + 1 }},
		{"no-aim", func(m *Match) { m.attackAims = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := aimedMatch(t)
			tc.prepare(m)
			events, _ := m.runUnits(nil)
			for _, event := range events {
				if event.Data["attackerId"] == "cat" {
					t.Fatalf("unexpected shot: %+v", event)
				}
			}
		})
	}
}

func TestAttackModesValidationAndDeterminism(t *testing.T) {
	unit := testScenario().Ruleset.Units[0]
	for _, mode := range []string{"", "tracking", "position", "unknown"} {
		unit.AttackMode = mode
		if validUnitType(unit) != (mode != "unknown") {
			t.Fatalf("mode %q", mode)
		}
	}
	first, _ := aimedMatch(t)
	second, _ := aimedMatch(t)
	a, _ := first.runUnits(nil)
	b, _ := second.runUnits(nil)
	if !reflect.DeepEqual(a, b) || !reflect.DeepEqual(first.State(), second.State()) {
		t.Fatal("nondeterministic resolution")
	}
}

func TestAttackOutcomeVisibilityAndReset(t *testing.T) {
	m, aim := aimedMatch(t)
	m.runUnit(&m.state.Units[0], nil)
	hidden := visibleUnit(m.state.Units[0], map[Position]bool{})
	if hidden.AttackOutcome != "" || hidden.AttackTargetPosition != nil || hidden.AttackTargetID != "" {
		t.Fatal("hidden impact leaked")
	}
	shown := visibleUnit(m.state.Units[0], map[Position]bool{aim: true})
	if shown.AttackOutcome != "hit" {
		t.Fatal("visible outcome missing")
	}
	m.clearUnitActions()
	if m.state.Units[0].AttackOutcome != "" {
		t.Fatal("stale outcome")
	}
}
