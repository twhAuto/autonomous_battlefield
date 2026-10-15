// Checks explicit recruitment quantities, blocked spawns, and default production behavior.

package game

import (
	"reflect"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
)

func TestExplicitRecruitmentOrders(t *testing.T) {
	tests := []struct {
		name          string
		gold, reserve int64
		orders        []controller.RecruitmentOrder
		want          []string
		remaining     int64
	}{
		{"mixed exact", 200, 20, []controller.RecruitmentOrder{{UnitType: "ranger", Count: 2}, {UnitType: "vanguard", Count: 1}}, []string{"ranger", "ranger", "vanguard"}, 105},
		{"empty means no purchases", 200, 0, []controller.RecruitmentOrder{}, []string{}, 200},
		{"partial quantity", 70, 20, []controller.RecruitmentOrder{{UnitType: "ranger", Count: 2}, {UnitType: "striker", Count: 1}}, []string{"ranger"}, 40},
		{"no cheaper substitution", 30, 0, []controller.RecruitmentOrder{{UnitType: "vanguard", Count: 1}, {UnitType: "striker", Count: 1}}, []string{}, 30},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, err := NewMatch("orders", testScenario(), 20)
			if err != nil {
				t.Fatal(err)
			}
			m.state.Units = nil
			m.factionState("faction-a").Gold = tc.gold
			plan := controller.PlanProposal{Name: "orders", ReserveGold: tc.reserve, RecruitmentOrders: &tc.orders}
			m.runRecruitment("faction-a", plan)
			got := []string{}
			for _, u := range m.state.Units {
				got = append(got, u.Type)
				if u.ReadyAtTick != m.state.Tick+1 {
					t.Fatal("recruit ready too early")
				}
			}
			if !reflect.DeepEqual(got, tc.want) || m.factionState("faction-a").Gold != tc.remaining {
				t.Fatalf("bought %v balance %d", got, m.factionState("faction-a").Gold)
			}
		})
	}
}
func TestExplicitRecruitmentHonorsBlockedSpawnsAndDefaults(t *testing.T) {
	m, err := NewMatch("spawns", testScenario(), 20)
	if err != nil {
		t.Fatal(err)
	}
	m.state.Units = nil
	for _, p := range Neighbors(m.castle("faction-a").Position) {
		m.state.Units = append(m.state.Units, Unit{FactionID: "faction-a", Position: p, Health: 1})
	}
	orders := []controller.RecruitmentOrder{{UnitType: "ranger", Count: 1}}
	plan := controller.PlanProposal{Name: "orders", RecruitmentOrders: &orders}
	before := m.factionState("faction-a").Gold
	events := m.runRecruitment("faction-a", plan)
	if len(events) != 1 || events[0].Data["reason"] != "no_spawn" || m.factionState("faction-a").Gold != before {
		t.Fatal("blocked spawn purchased a unit")
	}
	normalized := m.normalizedFactionPlan("faction-a", plan)
	if len(normalized.RecruitmentPriorities) != 0 || normalized.RecruitmentOrders == nil {
		t.Fatal("orders converted to legacy priorities")
	}
	orders[0].UnitType = "unknown"
	if validateProductionPlan(m.scenario, m.scenario.Factions[0], plan) == nil {
		t.Fatal("unknown order unit accepted")
	}
}
