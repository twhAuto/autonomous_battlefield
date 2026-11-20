// Checks authored unit additions reach the loaded ruleset and knowledge catalog.

package catalog

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/twhAuto/autonomous_battlefield/backend/gamedata"
	service "github.com/twhAuto/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/game"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/game/controller"
	"go.yaml.in/yaml/v3"
)

func TestAdditionalUnitFromYAML(t *testing.T) {
	dir, rules, unit := writeRulesetsWithSentinel(t)
	catalog, err := LoadCatalog(os.DirFS(dir))
	if err != nil {
		t.Fatal(err)
	}
	bp := game.Blueprint{Units: []game.UnitSelection{{Type: unit.ID, Count: 1}}, RecruitmentPriority: []string{unit.ID}}
	identities := [2]service.FactionIdentity{{ID: "a", Name: "A", RulesetID: rules.ID, Blueprint: bp}, {ID: "b", Name: "B", RulesetID: rules.ID, Blueprint: bp}}
	scenario, err := catalog.Resolve("castle-siege", identities)
	if err != nil {
		t.Fatal(err)
	}
	digest := digestOf(t, scenario)
	old := game.CloneScenario(scenario)
	catalog.rulesets[rules.ID].Knowledge.Cards[0].Role = "edited"
	nextScenario, err := catalog.Resolve("castle-siege", identities)
	next := digestOf(t, nextScenario)
	if err != nil || next == digest || old.Ruleset.Knowledge.Cards[0].Role != scenario.Ruleset.Knowledge.Cards[0].Role {
		t.Fatal("snapshot/hash isolation")
	}
	checkSentinelIsRecruited(t, scenario, unit.ID)
}

// writeRulesetsWithSentinel copies the game content to a temporary directory whose
// resolved rulesets give castle-siege a cheap "sentinel" unit and its knowledge card.
func writeRulesetsWithSentinel(t *testing.T) (string, game.Ruleset, game.UnitType) {
	t.Helper()
	seed, err := LoadCatalog(gamedata.Files)
	if err != nil {
		t.Fatal(err)
	}
	rules, _ := seed.Ruleset("battlefield")
	unit := rules.Units[0]
	unit.ID = "sentinel"
	unit.Cost = 10
	rules.Units = append(rules.Units, unit)
	rules.Buildings.CastleTrains = append(rules.Buildings.CastleTrains, unit.ID)
	for i := range rules.Structures {
		if rules.Structures[i].Kind == game.BuildingBarracks {
			rules.Structures[i].Trains = append(rules.Structures[i].Trains, unit.ID)
		}
	}
	card := rules.Knowledge.Cards[0]
	card.UnitID = unit.ID
	rules.Knowledge.Cards = append(rules.Knowledge.Cards, card)
	dir := copyGameData(t)
	rulesets := filepath.Join(dir, "rulesets")
	if err := os.RemoveAll(rulesets); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(rulesets, 0o700); err != nil {
		t.Fatal(err)
	}
	writeYAML(t, filepath.Join(rulesets, "rules.yaml"), rules)
	// Preserve the other authored rulesets when overriding only the v1 fixture.
	for _, other := range seed.Rulesets() {
		if other.ID != rules.ID {
			writeYAML(t, filepath.Join(rulesets, other.ID+".yaml"), other)
		}
	}
	return dir, rules, unit
}

func writeYAML(t *testing.T, path string, value any) {
	t.Helper()
	data, err := yaml.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

// checkSentinelIsRecruited plays to the first decision window and requires both
// factions' recruitment plans to produce the new unit.
func checkSentinelIsRecruited(t *testing.T, scenario game.Scenario, unitID string) {
	t.Helper()
	match, err := game.NewMatch("new-unit", scenario, 100)
	if err != nil {
		t.Fatal(err)
	}
	for range scenario.DecisionIntervalTicks {
		if _, err := match.Advance(); err != nil {
			t.Fatal(err)
		}
	}
	plans := map[string]controller.PlanProposal{}
	for _, id := range []string{"a", "b"} {
		orders := []controller.ProductionOrder{{BuildingID: id + ":castle", UnitType: unitID, Enabled: true}}
		plans[id] = controller.PlanProposal{Name: "recruit", ProductionOrders: &orders, ActionWeights: map[string]int64{"attack": 1000}}
	}
	if _, err := match.AdvanceWithPlans(plans); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 40 && !match.Finished(); i++ {
		if _, err := match.AdvanceWithPlans(nil); err != nil {
			t.Fatal(err)
		}
	}
	found := false
	for _, u := range match.State().Units {
		if u.Type == unitID {
			found = true
		}
	}
	if !found || match.State().Factions[0].UnitsRecruited == 0 {
		t.Fatal("new unit not recruited")
	}
}
