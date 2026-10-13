// Checks pinned content-library reuse, scenario restrictions, legacy inline rules, and
// rejection of invalid content.

package catalog

import (
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/briefing"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/decision"
	"github.com/twh09/autonomous_battlefield/backend/internal/testutil"

	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	service "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
	"go.yaml.in/yaml/v3"
)

func libraryFixture(t *testing.T) (string, template) {
	t.Helper()
	root := filepath.Join("..", "..", "..")
	dir := t.TempDir()
	for _, sub := range []string{"content", "rulesets", "scenarios"} {
		{
			err := os.Mkdir(filepath.Join(dir, sub), 0700)
			testutil.Require(t, err == nil, func() { t.Fatal(err) })
		}
	}
	for _, name := range []string{"content/core-v1.yaml", "rulesets/castle-siege-v1.yaml", "scenarios/castle-siege-v1.yaml"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		testutil.Require(t, err == nil, func() { t.Fatal(err) })
		{
			err = os.WriteFile(filepath.Join(dir, name), data, 0600)
			testutil.Require(t, err == nil, func() { t.Fatal(err) })
		}
	}
	var tmpl template
	{
		err := decodeOne(filepath.Join(dir, "scenarios/castle-siege-v1.yaml"), &tmpl)
		testutil.Require(t, err == nil, func() { t.Fatal(err) })
	}
	return dir, tmpl
}
func writeTemplate(t *testing.T, dir string, tmpl template) {
	t.Helper()
	data, err := yaml.Marshal(tmpl)
	testutil.Require(t, err == nil, func() { t.Fatal(err) })
	var fields map[string]any
	{
		err = yaml.Unmarshal(data, &fields)
		testutil.Require(t, err == nil, func() { t.Fatal(err) })
	}
	if tmpl.AllowedUnits == nil {
		delete(fields, "allowedUnits")
	}
	if tmpl.AllowedUpgrades == nil {
		delete(fields, "allowedUpgrades")
	}
	if tmpl.AllowedBuildings == nil {
		delete(fields, "allowedBuildings")
	}
	data, err = yaml.Marshal(fields)
	testutil.Require(t, err == nil, func() { t.Fatal(err) })
	{
		err = os.WriteFile(filepath.Join(dir, "scenarios", tmpl.ID+".yaml"), data, 0600)
		testutil.Require(t, err == nil, func() { t.Fatal(err) })
	}
}
func TestScenarioContentRestrictionsReachEngineAndAI(t *testing.T) {
	dir, tmpl := libraryFixture(t)
	tmpl.ID = "restricted"
	tmpl.AllowedUnits = []string{"ranger"}
	tmpl.AllowedUpgrades = []string{}
	tmpl.AllowedBuildings = []string{"castle"}
	writeTemplate(t, dir, tmpl)
	c, err := LoadCatalog(filepath.Join(dir, "scenarios"))
	testutil.Require(t, err == nil, func() { t.Fatal(err) })
	bp := game.Blueprint{Units: []game.UnitSelection{{Type: "ranger", Count: 1}}, RecruitmentPriority: []string{"ranger"}}
	ids := [2]service.FactionIdentity{{ID: "a", Name: "A", RulesetID: tmpl.RulesetID, Blueprint: bp}, {ID: "b", Name: "B", RulesetID: tmpl.RulesetID, Blueprint: bp}}
	scenario, hash, err := c.Resolve("restricted", ids)
	testutil.Require(t, err == nil, func() { t.Fatal(err) })
	testutil.Require(t, !(len(scenario.Ruleset.Units) != 1 || len(scenario.Ruleset.Upgrades) != 0 || scenario.Ruleset.Buildings.MaximumEconomyCount != 0 || scenario.Ruleset.ContentSource.Digest == ""), func() { t.Fatal("restrictions not resolved") })
	m, err := game.NewMatch("restricted", scenario, 20)
	testutil.Require(t, err == nil, func() { t.Fatal(err) })
	snapshot, err := m.DecisionSnapshot("a")
	testutil.Require(t, err == nil, func() { t.Fatal(err) })
	testutil.Require(t, !(len(snapshot.LegalUnits) != 1 || len(snapshot.LegalSites) != 0), func() { t.Fatal("excluded content in legal snapshot") })
	ctx := ai.DecisionContext{FactionID: "a", Legal: ai.LegalChoices{Units: snapshot.LegalUnits, ActionKinds: []string{"attack"}}}
	knowledge, err := briefing.RetrieveKnowledge(scenario.Ruleset, ctx, ai.DefaultKnowledgeLimits())
	testutil.Require(t, !(err != nil || len(knowledge.Cards) != 1 || knowledge.SelectedIDs[0] != "ranger"), func() { t.Fatal("knowledge leaked excluded cards") })
	orders := []controller.RecruitmentOrder{{UnitType: "striker", Count: 1}}
	submission := ai.PlanSubmission{IdempotencyKey: "once", ExpectedVersion: 1, TargetTick: 10, Plan: controller.PlanProposal{Name: "invalid", RecruitmentOrders: &orders}}
	testutil.Require(t, len(decision.ValidateSubmission(ai.DecisionRecord{Version: 1, TargetTick: 10, Observation: ctx}, submission)) != 0, func() { t.Fatal("AI accepted excluded purchase") })
	for range 10 {
		{
			_, err = m.Advance()
			testutil.Require(t, err == nil, func() { t.Fatal(err) })
		}
	}
	{
		_, err = m.AdvanceWithPlans(map[string]controller.PlanProposal{"a": submission.Plan})
		testutil.Require(t, err != nil, func() { t.Fatal("engine accepted excluded purchase") })
	}
	{
		_, err = m.Advance()
		testutil.Require(t, err == nil, func() { t.Fatal(err) })
	}
	for _, b := range m.State().Buildings {
		testutil.Require(t, b.Type != game.BuildingEconomy, func() { t.Fatal("disabled economy built") })
	}
	bad := ids
	bad[0].Blueprint = game.Blueprint{Units: []game.UnitSelection{{Type: "striker", Count: 1}}, RecruitmentPriority: []string{"striker"}}
	{
		_, _, err = c.Resolve("restricted", bad)
		testutil.Require(t, err == nil, func() { t.Fatalf("incompatible unit blueprint: %v", err) })
	}
	bad = ids
	bad[0].Blueprint.UpgradeIDs = []string{"arsenal"}
	{
		_, _, err = c.Resolve("restricted", bad)
		testutil.Require(t, err == nil, func() { t.Fatalf("incompatible upgrade blueprint: %v", err) })
	}
	// Editing library source changes new snapshots, never existing ones.
	path := filepath.Join(dir, "content/core-v1.yaml")
	var library contentLibrary
	_ = decodeOne(path, &library)
	library.Units[2].Cost++
	data, _ := yaml.Marshal(library)
	_ = os.WriteFile(path, data, 0600)
	next, err := LoadCatalog(filepath.Join(dir, "scenarios"))
	testutil.Require(t, err == nil, func() { t.Fatal(err) })
	changed, nextHash, err := next.Resolve("restricted", ids)
	testutil.Require(t, err == nil, func() { t.Fatal(err) })
	testutil.Require(t, !(nextHash == hash || changed.Ruleset.Units[0].Cost == scenario.Ruleset.Units[0].Cost || m.Scenario().Ruleset.Units[0].Cost != scenario.Ruleset.Units[0].Cost), func() { t.Fatal("snapshot immutability") })
	for _, s := range c.Summaries() {
		testutil.Require(t, !(s.ID == "restricted" && (!reflect.DeepEqual(s.AllowedUnits, []string{"ranger"}) || len(s.AllowedUpgrades) != 0 || !reflect.DeepEqual(s.AllowedBuildings, []string{"castle"}))), func() { t.Fatal("summary differs from restrictions") })
	}
	data, _ = json.Marshal(scenario)
	var restored game.Scenario
	{
		err = json.Unmarshal(data, &restored)
		testutil.Require(t, err == nil, func() { t.Fatal(err) })
	}
	{
		_, err = game.NewMatch("restored", restored, 20)
		testutil.Require(t, err == nil, func() { t.Fatal("snapshot needs external library", err) })
	}
}
func TestInvalidScenarioRestrictionsFailAtStartup(t *testing.T) {
	changes := []func(*template){
		func(t *template) { t.AllowedUnits = []string{} }, func(t *template) { t.AllowedUnits = []string{"unknown"} }, func(t *template) { t.AllowedUnits = []string{"ranger", "ranger"} }, func(t *template) { t.AllowedUpgrades = []string{"unknown"} }, func(t *template) { t.AllowedBuildings = []string{"economy"} }, func(t *template) { t.AllowedBuildings = []string{"castle", "unknown"} },
		func(t *template) {
			t.AllowedUnits = []string{"ranger"}
			t.FallbackPlan.RecruitmentPriorities = []string{"striker"}
		}, func(t *template) {
			t.AllowedBuildings = []string{"castle"}
			t.FallbackPlan.ConstructionPriorities = []controller.Coordinate{{X: 2, Y: 2}}
		},
	}
	for i, change := range changes {
		t.Run(string(rune('a'+i)), func(t *testing.T) {
			dir, tmpl := libraryFixture(t)
			change(&tmpl)
			writeTemplate(t, dir, tmpl)
			{
				_, err := LoadCatalog(filepath.Join(dir, "scenarios"))
				testutil.Require(t, err != nil, func() { t.Fatal("invalid restriction accepted") })
			}
		})
	}
}
func TestLibraryReferencesAndLegacyInlineCompatibility(t *testing.T) {
	dir, _ := libraryFixture(t)
	var file rulesetFile
	path := filepath.Join(dir, "rulesets/castle-siege-v1.yaml")
	{
		err := decodeOne(path, &file)
		testutil.Require(t, err == nil, func() { t.Fatal(err) })
	}
	baseline, err := resolveLibrary(filepath.Join(dir, "rulesets"), file)
	testutil.Require(t, err == nil, func() { t.Fatal(err) })
	inline, err := resolveLibrary(dir, rulesetFile{Ruleset: baseline})
	testutil.Require(t, !(err != nil || !reflect.DeepEqual(baseline, inline)), func() { t.Fatal("legacy inline broken") })
	for _, change := range []func(*rulesetFile){func(f *rulesetFile) { f.Library.ID = "../bad" }, func(f *rulesetFile) { f.Library.Version = "missing" }, func(f *rulesetFile) { f.Library.Castle = "economy" }, func(f *rulesetFile) { f.Library.Economy = "missing" }, func(f *rulesetFile) { f.Library.Units = []string{"missing"} }, func(f *rulesetFile) { f.Library.Upgrades = []string{"missing"} }, func(f *rulesetFile) { f.Units = baseline.Units }, func(f *rulesetFile) { f.Buildings.CastleHealth = 100 }} {
		copy := file
		ref := *file.Library
		copy.Library = &ref
		change(&copy)
		{
			_, err := resolveLibrary(filepath.Join(dir, "rulesets"), copy)
			testutil.Require(t, err != nil, func() { t.Fatal("invalid library reference accepted") })
		}
	}
}

func TestLibraryValidatesUnselectedContent(t *testing.T) {
	dir, _ := libraryFixture(t)
	var l contentLibrary
	{
		err := decodeOne(filepath.Join(dir, "content/core-v1.yaml"), &l)
		testutil.Require(t, err == nil, func() { t.Fatal(err) })
	}
	for _, change := range []func(*contentLibrary){
		func(l *contentLibrary) { l.Units[0].Cost = 0 }, func(l *contentLibrary) { l.Upgrades[0].Cost = 0 }, func(l *contentLibrary) { l.Buildings[0].Kind = "healer" }, func(l *contentLibrary) { l.Buildings[1].Cost = 0 }, func(l *contentLibrary) { l.Buildings = append(l.Buildings, l.Buildings[0]) }, func(l *contentLibrary) { l.Knowledge.Cards[0].UnitID = "unknown" },
	} {
		var copy contentLibrary
		data, _ := yaml.Marshal(l)
		_ = yaml.Unmarshal(data, &copy)
		change(&copy)
		testutil.Require(t, validateLibrary(copy) != nil, func() { t.Fatal("invalid library entry accepted") })
	}
}

func TestRulesetsReusePinnedLibraryWithDifferentSelections(t *testing.T) {
	dir, _ := libraryFixture(t)
	var file rulesetFile
	{
		err := decodeOne(filepath.Join(dir, "rulesets/castle-siege-v1.yaml"), &file)
		testutil.Require(t, err == nil, func() { t.Fatal(err) })
	}
	a, err := resolveLibrary(filepath.Join(dir, "rulesets"), file)
	testutil.Require(t, err == nil, func() { t.Fatal(err) })
	file.ID = "ranged"
	file.Library.Units = []string{"ranger"}
	file.Library.Upgrades = []string{}
	b, err := resolveLibrary(filepath.Join(dir, "rulesets"), file)
	testutil.Require(t, err == nil, func() { t.Fatal(err) })
	testutil.Require(t, !(len(b.Units) != 1 || len(b.Upgrades) != 0 || b.Units[0] != a.Units[2] || *b.ContentSource != *a.ContentSource), func() { t.Fatal("library entry not reused") })
	b.Knowledge.Cards[0].Role = "changed"
	testutil.Require(t, b.Knowledge.Cards[0].Role != a.Knowledge.Cards[2].Role, func() { t.Fatal("shared mutable knowledge") })
}
