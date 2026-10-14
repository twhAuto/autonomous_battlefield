// Checks strict catalog loading, resolved faction snapshots, unknown scenarios, and
// malformed scenario YAML.

package catalog

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/twh09/autonomous_battlefield/backend/gamedata"
	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
)

func TestCatalogLoadsRulesetAndResolvesBlueprints(t *testing.T) {
	catalog, err := LoadCatalog(gamedata.Files)
	if err != nil {
		t.Fatal(err)
	}
	blueprint := game.Blueprint{Units: []game.UnitSelection{{Type: "vanguard", Count: 1}, {Type: "striker", Count: 1}, {Type: "ranger", Count: 1}}, UpgradeIDs: []string{"arsenal"}, RecruitmentPriority: []string{"vanguard", "striker", "ranger"}}
	identities := [2]battleservice.FactionIdentity{{ID: "faction-a", Name: "A", RulesetID: "castle-siege-v1", Blueprint: blueprint}, {ID: "faction-b", Name: "B", RulesetID: "castle-siege-v1", Blueprint: blueprint}}
	first, err := catalog.Resolve("castle-siege-v1", identities)
	if err != nil {
		t.Fatal(err)
	}
	digest := digestOf(t, first)
	second, err := catalog.Resolve("castle-siege-v1", identities)
	if err != nil {
		t.Fatal(err)
	}
	nextDigest := digestOf(t, second)
	if digest == "" || digest != nextDigest || first.Map.Width != 15 || second.Map.Height != 11 {
		t.Fatalf("unexpected resolutions: %#v", first)
	}
	if summaries := catalog.Summaries(); len(summaries) != 3 || summaries[0].RulesetID != "castle-siege-v1" || summaries[0].MapWidth != 15 || summaries[0].MaximumTicks != 500 {
		t.Fatalf("summaries = %#v", summaries)
	}
	if rulesets := catalog.Rulesets(); len(rulesets) != 2 || rulesets[0].DesignBudget != 0 || rulesets[0].StartingGold != 180 {
		t.Fatalf("rulesets = %#v", rulesets)
	}
	if ruleset, ok := catalog.Ruleset("castle-siege-v1"); !ok || ruleset.ID != "castle-siege-v1" {
		t.Fatalf("Ruleset() = %#v, %v", ruleset, ok)
	}
	if _, ok := catalog.Ruleset("missing"); ok {
		t.Fatal("missing ruleset was reported present")
	}
}

func TestCatalogRejectsUnknownScenarioField(t *testing.T) {
	dir := copyGameData(t)
	contents, err := fs.ReadFile(gamedata.Files, "scenarios/castle-siege-v1.yaml")
	if err != nil {
		t.Fatal(err)
	}
	contents = append(contents, []byte("\nunknown: true\n")...)
	if err := os.WriteFile(filepath.Join(dir, "scenarios", "bad.yaml"), contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCatalog(os.DirFS(dir)); err == nil {
		t.Fatal("unknown scenario field accepted")
	}
}

func TestCatalogRejectsScenarioRulesVersionMismatch(t *testing.T) {
	dir := copyGameData(t)
	contents, err := fs.ReadFile(gamedata.Files, "scenarios/castle-siege-v1.yaml")
	if err != nil {
		t.Fatal(err)
	}
	contents = bytes.Replace(contents, []byte(`rulesVersion: "1"`), []byte(`rulesVersion: "2"`), 1)
	if err := os.WriteFile(filepath.Join(dir, "scenarios", "castle-siege-v1.yaml"), contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCatalog(os.DirFS(dir)); !errors.Is(err, ErrInvalidScenario) || !strings.Contains(err.Error(), "rules version") {
		t.Fatalf("mismatched rules version: %v", err)
	}
}

func TestCatalogReportsMissingDirectories(t *testing.T) {
	for _, missing := range []string{"rulesets", "scenarios"} {
		dir := copyGameData(t)
		if err := os.RemoveAll(filepath.Join(dir, missing)); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadCatalog(os.DirFS(dir)); err == nil || !strings.Contains(err.Error(), missing[:len(missing)-1]) {
			t.Fatalf("missing %s: %v", missing, err)
		}
	}
}

// copyGameData copies the embedded game content into a temporary directory that a
// test can change.
func copyGameData(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.CopyFS(dir, gamedata.Files); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCatalogAcceptsGenericFaction(t *testing.T) {
	catalog, err := LoadCatalog(gamedata.Files)
	if err != nil {
		t.Fatal(err)
	}
	_, err = catalog.Resolve("castle-siege-v1", [2]battleservice.FactionIdentity{{ID: "a", Name: "A", Doctrine: "Defend", RulesetID: "other"}, {ID: "b", Name: "B", Doctrine: "Attack", RulesetID: "other"}})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
}

func TestCatalogReturnsNotFound(t *testing.T) {
	catalog, err := LoadCatalog(gamedata.Files)
	if err != nil {
		t.Fatal(err)
	}
	_, err = catalog.Resolve("missing", [2]battleservice.FactionIdentity{})
	if !errors.Is(err, battleservice.ErrScenarioNotFound) {
		t.Fatalf("error = %v", err)
	}
}

func FuzzScenarioYAML(f *testing.F) {
	seed, err := fs.ReadFile(gamedata.Files, "scenarios/castle-siege-v1.yaml")
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Add([]byte("id: fuzz\nunknown: true\n"))
	f.Fuzz(func(t *testing.T, value []byte) {
		if len(value) > 64<<10 {
			t.Skip()
		}
		var scenario game.Scenario
		_ = decodeOne(fstest.MapFS{"scenario.yaml": {Data: value}}, "scenario.yaml", &scenario)
	})
}

func TestDoctrineSnapshotIsIndependentOfFutureProfileEdits(t *testing.T) {
	catalog, err := LoadCatalog(gamedata.Files)
	if err != nil {
		t.Fatal(err)
	}
	identities := [2]battleservice.FactionIdentity{{ID: "a", Name: "A", Doctrine: "Scout", Background: "Old roots"}, {ID: "b", Name: "B", Doctrine: "Defend"}}
	first, err := catalog.Resolve("castle-siege-v1", identities)
	if err != nil {
		t.Fatal(err)
	}
	hash := digestOf(t, first)
	identities[0].Name = "Edited"
	identities[0].Doctrine = "Attack"
	identities[0].Background = "A new history"
	updatedScenario, err := catalog.Resolve("castle-siege-v1", identities)
	if err != nil {
		t.Fatal(err)
	}
	updated := digestOf(t, updatedScenario)
	if first.Factions[0].Background != "Old roots" || first.Factions[0].Name != "A" || first.Factions[0].Doctrine != "Scout" || hash == updated || first.SnapshotVersion != game.DoctrineSnapshotVersion {
		t.Fatal("profile snapshot not immutable")
	}
	for _, summary := range catalog.Summaries() {
		if _, err := catalog.Resolve(summary.ID, identities); err != nil {
			t.Fatal("generic profiles cannot cross scenarios", err)
		}
	}
}

// digestOf fingerprints a resolved scenario with the battle service's digest function.
func digestOf(t *testing.T, scenario game.Scenario) string {
	t.Helper()
	digest, err := battleservice.ScenarioDigest(scenario)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func TestCatalogRejectsBrokenContentAtStartup(t *testing.T) {
	for _, test := range []struct {
		name, want string
		change     func(t *testing.T, dir string)
	}{
		{"malformed ruleset", "decode catalog file", func(t *testing.T, dir string) {
			writeCatalogFile(t, dir, "rulesets/broken.yaml", "id: [")
		}},
		{"missing library version", "content/core-v9.yaml", func(t *testing.T, dir string) {
			editedRuleset(t, dir, "other.yaml", `id: other`, `  version: "1"`, `  version: "9"`)
		}},
		{"invalid ruleset values", "ruleset \"rulesets/other.yaml\"", func(t *testing.T, dir string) {
			editedRuleset(t, dir, "other.yaml", `id: other`, `startingGold: 180`, `startingGold: -1`)
		}},
		{"duplicate ruleset", `duplicate ruleset ID "castle-siege-v1"`, func(t *testing.T, dir string) {
			copyCatalogFile(t, dir, "rulesets/castle-siege-v1.yaml", "rulesets/copy.yaml")
		}},
		{"no rulesets", "rulesets contains no YAML rulesets", func(t *testing.T, dir string) {
			replaceWithNotes(t, dir, "rulesets")
		}},
		{"duplicate scenario", `duplicate scenario ID "castle-siege-v1"`, func(t *testing.T, dir string) {
			copyCatalogFile(t, dir, "scenarios/castle-siege-v1.yaml", "scenarios/copy.yaml")
		}},
		{"no scenarios", "scenarios contains no YAML scenarios", func(t *testing.T, dir string) {
			replaceWithNotes(t, dir, "scenarios")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := copyGameData(t)
			test.change(t, dir)
			if _, err := LoadCatalog(os.DirFS(dir)); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want it to mention %q", err, test.want)
			}
		})
	}
}

func TestCatalogSkipsFoldersAndNonYAMLFiles(t *testing.T) {
	dir := copyGameData(t)
	for _, folder := range []string{"rulesets", "scenarios"} {
		writeCatalogFile(t, dir, folder+"/notes.txt", "not YAML")
		if err := os.Mkdir(filepath.Join(dir, folder, "drafts.yaml"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	catalog, err := LoadCatalog(os.DirFS(dir))
	if err != nil || len(catalog.IDs()) != 3 || len(catalog.Rulesets()) != 2 {
		t.Fatalf("catalog = %v, %v", catalog, err)
	}
}

func writeCatalogFile(t *testing.T, dir, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func copyCatalogFile(t *testing.T, dir, from, to string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, from))
	if err != nil {
		t.Fatal(err)
	}
	writeCatalogFile(t, dir, to, string(data))
}

// editedRuleset adds castle-siege-v1 as a new ruleset file under a new ID, with one
// line replaced.
func editedRuleset(t *testing.T, dir, name, id, old, replacement string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "rulesets", "castle-siege-v1.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Replace(string(data), "id: castle-siege-v1", id, 1)
	if !strings.Contains(text, old) {
		t.Fatalf("ruleset has no %q", old)
	}
	writeCatalogFile(t, dir, "rulesets/"+name, strings.Replace(text, old, replacement, 1))
}

// replaceWithNotes empties a catalog folder except for a non-YAML file.
func replaceWithNotes(t *testing.T, dir, folder string) {
	t.Helper()
	if err := os.RemoveAll(filepath.Join(dir, folder)); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, folder), 0o700); err != nil {
		t.Fatal(err)
	}
	writeCatalogFile(t, dir, folder+"/README.md", "notes")
}
