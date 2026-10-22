package game

import (
	"reflect"
	"slices"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
)

func specializedMatch(t *testing.T) *Match {
	t.Helper()
	scenario := doctrineMatch(t).Scenario()
	scenario.Ruleset.Buildings.MaximumBuildingCount = 8
	scenario.Ruleset.Structures = []BuildingType{
		{ID: BuildingBarracks, Kind: BuildingBarracks, Health: 100, Cost: 90, BuildRange: 3, Trains: []string{"vanguard", "striker"}},
		{ID: BuildingArcheryRange, Kind: BuildingArcheryRange, Health: 100, Cost: 90, BuildRange: 3, Trains: []string{"ranger"}},
	}
	m, err := NewMatch("specialized", scenario, 100)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSpecializedOpeningAndSnapshotIsolation(t *testing.T) {
	m := specializedMatch(t)
	for _, id := range m.FactionIDs() {
		snapshot, err := m.DecisionSnapshot(id)
		if err != nil || len(snapshot.RecruitmentSources) != 1 || len(m.castle(id).Trains) != 0 {
			t.Fatal("opening sources", snapshot, err)
		}
		source := m.recruitmentSource(id, snapshot.RecruitmentSources[0])
		if source.Type != BuildingBarracks || source.ReadyAtTick != 0 || HexDistance(source.Position, m.castle(id).Position) < 2 || m.factionState(id).Gold != 180 || m.factionState(id).BuildingsBuilt != 0 {
			t.Fatal("invalid free starting Barracks", source)
		}
		for _, order := range *m.OpeningFallback(id).RecruitmentOrders {
			if !slices.Contains(source.Trains, order.UnitType) {
				t.Fatal("untrainable opening order", order)
			}
		}
	}
	copy := m.State()
	for i := range copy.Buildings {
		if len(copy.Buildings[i].Trains) > 0 {
			copy.Buildings[i].Trains[0] = "changed"
		}
	}
	scenario := m.Scenario()
	scenario.Ruleset.Structures[0].Trains[0] = "changed"
	legal := ConstructibleBuildings(m.scenario.Ruleset)
	legal[len(legal)-2].Trains[0] = "changed"
	if m.scenario.Ruleset.Structures[0].Trains[0] != "vanguard" || m.defaultTrainingSource("faction-a", "vanguard").Trains[0] != "vanguard" {
		t.Fatal("training roster aliases snapshot")
	}
	transition, err := m.Advance()
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range transition.State.Buildings {
		if b.Type == BuildingCastle && len(b.TrainingQueue) > 0 {
			t.Fatal("castle trained")
		}
		if b.Type == BuildingBarracks && len(b.TrainingQueue) == 0 {
			t.Fatal("opening did not train")
		}
	}
}

func TestTrainingSourceEligibilityAndActivation(t *testing.T) {
	m := specializedMatch(t)
	id := "faction-a"
	m.factionState(id).Gold = 1000
	source := m.defaultTrainingSource(id, "vanguard")
	for _, tc := range []struct{ source, unit, reason string }{
		{id + ":castle", "vanguard", "source_unavailable"},
		{source.ID, "ranger", "unit_not_trainable"},
		{"faction-b:barracks:starting", "vanguard", "source_unavailable"},
		{"", "ranger", "source_unavailable"},
	} {
		orders := []controller.RecruitmentOrder{{UnitType: tc.unit, Count: 1, SourceBuildingID: tc.source}}
		before := m.factionState(id).Gold
		events := m.runRecruitment(id, controller.PlanProposal{RecruitmentOrders: &orders})
		if len(events) != 1 || events[0].Data["reason"] != tc.reason || m.factionState(id).Gold != before {
			t.Fatalf("bad order %+v: %+v", tc, events)
		}
	}
	site := m.legalConstructionSites(id)[0]
	built, reason := m.buildStructureAt(id, BuildingArcheryRange, Position{site.X, site.Y}, 0)
	if reason != "" || !reflect.DeepEqual(built.Trains, []string{"ranger"}) {
		t.Fatal("build range", built, reason)
	}
	if m.defaultTrainingSource(id, "ranger") != nil || slices.Contains(m.recruitmentSources(id), built.ID) {
		t.Fatal("source active on construction tick")
	}
	m.state.Tick++
	queueUnits(t, m, "", "ranger")
	if len(m.recruitmentSource(id, built.ID).TrainingQueue) != 1 {
		t.Fatal("automatic source did not select range")
	}
	m.recruitmentSource(id, built.ID).Health = 0
	m.runTraining()
	if slices.Contains(m.recruitmentSources(id), built.ID) || m.defaultTrainingSource(id, "ranger") != nil {
		t.Fatal("destroyed source available")
	}
	for _, b := range m.state.Buildings {
		if b.ID == built.ID && len(b.TrainingQueue) != 0 {
			t.Fatal("destroyed queue retained")
		}
	}
}

func TestAutomaticRecruitmentSkipsFullQueuesAndRespectsReserve(t *testing.T) {
	m := specializedMatch(t)
	id := "faction-a"
	m.factionState(id).Gold = 1000
	first := m.defaultTrainingSource(id, "vanguard")
	another := *first
	another.ID = id + ":barracks:second"
	m.state.Buildings = append(m.state.Buildings, another)
	first = m.recruitmentSource(id, first.ID)
	first.TrainingQueue = make([]TrainingOrder, MaximumTrainingQueue)
	queueUnits(t, m, "", "vanguard")
	if len(m.recruitmentSource(id, another.ID).TrainingQueue) != 1 {
		t.Fatal("full queue did not route to another compatible source")
	}
	before := m.factionState(id).Gold
	orders := []controller.RecruitmentOrder{{UnitType: "vanguard", Count: 1}}
	events := m.runRecruitment(id, controller.PlanProposal{RecruitmentOrders: &orders, ReserveGold: before})
	if events[0].Data["reason"] != "reserve_required" || m.factionState(id).Gold != before {
		t.Fatal("reserve ignored", events)
	}
}

func TestTrainingRosterRejectsUnknownDuplicateAndNonProductionAssignments(t *testing.T) {
	for _, mutate := range []func(*Ruleset){
		func(r *Ruleset) { r.Structures[0].Trains = append(r.Structures[0].Trains, "unknown") },
		func(r *Ruleset) { r.Structures[1].Trains = append(r.Structures[1].Trains, "vanguard") },
		func(r *Ruleset) { r.Structures[0].ID = BuildingOutpost; r.Structures[0].Kind = BuildingOutpost },
	} {
		r := specializedMatch(t).Scenario().Ruleset
		mutate(&r)
		if ValidateRuleset(r) == nil {
			t.Fatal("invalid roster accepted")
		}
	}
}
