// Checks a shared ruleset on every map, strict content validation and frozen snapshots.
package catalog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/gamedata"
	service "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"go.yaml.in/yaml/v3"
)

func TestEveryMapUsesSharedContent(t *testing.T) {
	c, err := LoadCatalog(gamedata.Files)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Rulesets()) != 1 || len(c.IDs()) != 2 {
		t.Fatal("expected one ruleset and two distinct maps")
	}
	var baseline game.Ruleset
	for i, id := range c.IDs() {
		s, err := c.Resolve(id, [2]service.FactionIdentity{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			baseline = s.Ruleset
		} else if !reflect.DeepEqual(baseline, s.Ruleset) {
			t.Fatal("map changes content")
		}
		checkSharedDecisionContent(t, s)
		summary := c.Summaries()[i]
		if !reflect.DeepEqual(summary.AllowedUnits, unitIDs(baseline)) || !reflect.DeepEqual(summary.AllowedUpgrades, upgradeIDs(baseline)) || !reflect.DeepEqual(summary.AllowedBuildings, buildingIDs(baseline)) {
			t.Fatal("summary differs from shared catalog")
		}
		checkSnapshotIsSelfContained(t, s)
	}
}

func TestLibraryEditsLeaveSnapshotsUnchanged(t *testing.T) {
	dir := copyGameData(t)
	ids := [2]service.FactionIdentity{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}}
	c, err := LoadCatalog(os.DirFS(dir))
	if err != nil {
		t.Fatal(err)
	}
	before, err := c.Resolve("castle-siege", ids)
	if err != nil {
		t.Fatal(err)
	}
	var library contentLibrary
	if err = decodeOne(os.DirFS(dir), "content/core-v7.yaml", &library); err != nil {
		t.Fatal(err)
	}
	library.Units[0].Cost++
	writeYAML(t, filepath.Join(dir, "content/core-v7.yaml"), library)
	next, err := LoadCatalog(os.DirFS(dir))
	if err != nil {
		t.Fatal(err)
	}
	after, err := next.Resolve("castle-siege", ids)
	if err != nil {
		t.Fatal(err)
	}
	if digestOf(t, before) == digestOf(t, after) || before.Ruleset.Units[0].Cost == after.Ruleset.Units[0].Cost {
		t.Fatal("snapshot aliasing")
	}
	checkSnapshotIsSelfContained(t, before)
}

func checkSnapshotIsSelfContained(t *testing.T, s game.Scenario) {
	t.Helper()
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var restored game.Scenario
	if err = json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if _, err = game.NewMatch("restored", restored, 20); err != nil {
		t.Fatal(err)
	}
}

func TestLibraryReferencesAndValidation(t *testing.T) {
	var source rulesetFile
	if err := decodeOne(gamedata.Files, "rulesets/battlefield.yaml", &source); err != nil {
		t.Fatal(err)
	}
	baseline, err := resolveLibrary(gamedata.Files, source)
	if err != nil {
		t.Fatal(err)
	}
	inline, err := resolveLibrary(gamedata.Files, rulesetFile{Ruleset: baseline})
	if err != nil || !reflect.DeepEqual(inline, baseline) {
		t.Fatal("inline snapshot rules changed")
	}
	for _, change := range []func(*rulesetFile){
		func(s *rulesetFile) { s.Library.ID = "../bad" }, func(s *rulesetFile) { s.Library.Version = "missing" },
		func(s *rulesetFile) { s.Library.Castle = "economy" }, func(s *rulesetFile) { s.Library.Economy = "missing" },
		func(s *rulesetFile) { s.Units = baseline.Units }, func(s *rulesetFile) { s.Buildings.CastleHealth = 100 },
	} {
		s := source
		ref := *source.Library
		s.Library = &ref
		change(&s)
		if _, err := resolveLibrary(gamedata.Files, s); err == nil {
			t.Fatal("invalid reference accepted")
		}
	}
	var library contentLibrary
	if err := decodeOne(gamedata.Files, "content/core-v7.yaml", &library); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*contentLibrary){
		func(l *contentLibrary) { l.Units[0].Cost = 0 }, func(l *contentLibrary) { l.Upgrades[0].Cost = 0 },
		func(l *contentLibrary) { l.Buildings[0].Kind = "healer" }, func(l *contentLibrary) { l.Buildings[1].Cost = 0 },
		func(l *contentLibrary) { l.Buildings = append(l.Buildings, l.Buildings[0]) }, func(l *contentLibrary) { l.Knowledge.Cards[0].UnitID = "missing" },
	} {
		var copy contentLibrary
		data, _ := yaml.Marshal(library)
		if err := yaml.Unmarshal(data, &copy); err != nil {
			t.Fatal(err)
		}
		change(&copy)
		if validateLibrary(copy) == nil {
			t.Fatal("invalid content accepted")
		}
	}
}

// Historical authored files live only under testdata. Production resolves saved
// battles from their complete scenario JSON, without loading an old catalog.
func legacyScenario(t *testing.T, id string) game.Scenario {
	t.Helper()
	fsys := os.DirFS("testdata/legacy")
	data, err := os.ReadFile("testdata/legacy/scenarios/" + id + ".yaml")
	if err != nil {
		t.Fatal(err)
	}
	var tmpl template
	if err = yaml.Unmarshal(data, &tmpl); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile("testdata/legacy/rulesets/" + tmpl.RulesetID + ".yaml")
	if err != nil {
		t.Fatal(err)
	}
	var source rulesetFile
	if err = yaml.Unmarshal(data, &source); err != nil {
		t.Fatal(err)
	}
	rules, err := resolveLibrary(fsys, source)
	if err != nil {
		t.Fatal(err)
	}
	c := Catalog{templates: map[string]template{id: tmpl}, rulesets: map[string]game.Ruleset{rules.ID: rules}}
	s, err := c.Resolve(id, [2]service.FactionIdentity{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}})
	if err != nil {
		t.Fatal(err)
	}
	// Resolve uses the stored plans and rules; cloned empty slices match historical snapshots.
	return s
}

func checkSharedDecisionContent(t *testing.T, s game.Scenario) {
	t.Helper()
	m, err := game.NewMatch("shared", s, 20)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := m.DecisionSnapshot("a")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot.LegalUnits, s.Ruleset.Units) || !reflect.DeepEqual(snapshot.LegalUpgrades, s.Ruleset.Upgrades) {
		t.Fatal("AI missing shared content")
	}
}

func TestCastleLibraryCapabilities(t *testing.T) {
	var library contentLibrary
	if err := decodeOne(gamedata.Files, "content/core-v7.yaml", &library); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*buildingDefinition){
		func(b *buildingDefinition) { b.Attack = -1 },
		func(b *buildingDefinition) { b.Range = 0 },
		func(b *buildingDefinition) { b.Trains = []string{"missing"} },
		func(b *buildingDefinition) { b.Trains = []string{"vanguard", "vanguard"} },
		func(b *buildingDefinition) { b.Kind = "economy" },
	} {
		copy := library
		copy.Buildings = append([]buildingDefinition(nil), library.Buildings...)
		mutate(&copy.Buildings[0])
		if validateLibrary(copy) == nil {
			t.Fatal("invalid castle capabilities accepted", copy.Buildings[0])
		}
	}
}

func TestCatalogCastleSharesBarracksRosterAndTowerAttack(t *testing.T) {
	c, err := LoadCatalog(gamedata.Files)
	if err != nil {
		t.Fatal(err)
	}
	rules, _ := c.Ruleset("battlefield")
	castle := rules.Buildings
	if !reflect.DeepEqual(castle.CastleTrains, []string{"vanguard", "striker", "pikeman"}) || castle.CastleAttack != 15 || castle.CastleRange != 3 {
		t.Fatal("castle capabilities", castle)
	}
	if castle.CastleHealth != 300 || castle.CastleDefense != 8 || castle.CastleIncome != 10 || castle.CastleBuildRange != 4 {
		t.Fatal("castle baseline changed", castle)
	}
}
