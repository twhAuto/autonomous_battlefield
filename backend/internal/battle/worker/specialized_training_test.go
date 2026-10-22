package worker

import (
	"context"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/gamedata"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/battle/catalog"
	service "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
)

func TestSharedCatalogScriptedProductionCompletes(t *testing.T) {
	for _, mapID := range []string{"castle-siege", "frontier-siege"} {
		t.Run(mapID, func(t *testing.T) { checkScriptedProduction(t, mapID) })
	}
}

func checkScriptedProduction(t *testing.T, mapID string) {
	t.Helper()
	content, err := catalog.LoadCatalog(gamedata.Files)
	if err != nil {
		t.Fatal(err)
	}
	scenario, err := content.Resolve(mapID, [2]service.FactionIdentity{{ID: "faction-a", Name: "A"}, {ID: "faction-b", Name: "B"}})
	if err != nil {
		t.Fatal(err)
	}
	scenario.TickDuration = 0
	for i := range scenario.Factions {
		scenario.Factions[i].Controller.Type = ai.ControllerScripted
	}
	scenario.Ruleset.Buildings.CastleHealth = 100000
	job := testJob(30)
	job.Scenario = scenario
	repo := &decisionBattleRepository{fakeBattleRepository: &fakeBattleRepository{}, records: map[string]ai.DecisionRecord{}}
	worker, err := New(repo, repo, testWorkerConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err = worker.process(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if repo.completed == nil || repo.completed.Tick != 30 {
		t.Fatal("scripted battle did not finish")
	}
	windows := map[uint64]bool{}
	for _, record := range repo.records {
		if record.Status != "accepted" || record.ResolvedPlan == nil {
			t.Fatal("scripted decision rejected", record.TargetTick, record.Status)
		}
		windows[record.TargetTick] = true
	}
	if !windows[0] || !windows[10] || !windows[20] || !windows[30] {
		t.Fatal("missing production windows", windows)
	}
}
