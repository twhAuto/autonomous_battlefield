package catalog

import (
	service "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
	"go.yaml.in/yaml/v3"
	"os"
	"path/filepath"
	"testing"
)

func TestAdditionalUnitFromYAML(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	seed, err := LoadCatalog(filepath.Join(root, "scenarios"), filepath.Join(root, "rulesets"))
	if err != nil {
		t.Fatal(err)
	}
	rules, _ := seed.Ruleset("castle-siege-v1")
	var data []byte
	unit := rules.Units[0]
	unit.ID = "sentinel"
	unit.Cost = 10
	rules.Units = append(rules.Units, unit)
	card := rules.Knowledge.Cards[0]
	card.UnitID = unit.ID
	rules.Knowledge.Cards = append(rules.Knowledge.Cards, card)
	dir := t.TempDir()
	data, _ = yaml.Marshal(rules)
	if err = os.WriteFile(filepath.Join(dir, "rules.yaml"), data, 0600); err != nil {
		t.Fatal(err)
	}
	catalog, err := LoadCatalog(filepath.Join(root, "scenarios"), dir)
	if err != nil {
		t.Fatal(err)
	}
	bp := game.Blueprint{Units: []game.UnitSelection{{Type: unit.ID, Count: 1}}, RecruitmentPriority: []string{unit.ID}}
	identities := [2]service.FactionIdentity{{ID: "a", Name: "A", RulesetID: rules.ID, Blueprint: bp}, {ID: "b", Name: "B", RulesetID: rules.ID, Blueprint: bp}}
	scenario, digest, err := catalog.Resolve("castle-siege-v1", identities)
	if err != nil {
		t.Fatal(err)
	}
	old := game.CloneScenario(scenario)
	catalog.rulesets[rules.ID].Knowledge.Cards[0].Role = "edited"
	_, next, err := catalog.Resolve("castle-siege-v1", identities)
	if err != nil || next == digest || old.Ruleset.Knowledge.Cards[0].Role != scenario.Ruleset.Knowledge.Cards[0].Role {
		t.Fatal("snapshot/hash isolation")
	}
	match, err := game.NewMatch("new-unit", scenario, 20)
	if err != nil {
		t.Fatal(err)
	}
	for range 9 {
		if _, err = match.Advance(); err != nil {
			t.Fatal(err)
		}
	}
	plan := controller.PlanProposal{Name: "recruit", SpendOrder: []string{"recruitment"}, RecruitmentPriorities: []string{unit.ID}, ActionWeights: map[string]int64{"attack": 1000}}
	transition, err := match.AdvanceWithPlans(map[string]controller.PlanProposal{"a": plan, "b": plan})
	if err != nil {
		t.Fatal(err)
	}
	_ = transition
	found := false
	for _, u := range match.State().Units {
		if u.Type == unit.ID {
			found = true
		}
	}
	if !found || match.State().Factions[0].UnitsRecruited == 0 {
		t.Fatal("new unit not recruited")
	}
}
