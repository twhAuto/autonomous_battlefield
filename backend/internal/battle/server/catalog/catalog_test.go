package catalog

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/server/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
)

func TestCatalogLoadsRulesetAndResolvesBlueprints(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..")
	catalog, err := LoadCatalog(filepath.Join(root, "scenarios"), filepath.Join(root, "rulesets"))
	if err != nil {
		t.Fatal(err)
	}
	blueprint := game.Blueprint{Units: []game.UnitSelection{{Type: "vanguard", Count: 1}, {Type: "striker", Count: 1}, {Type: "ranger", Count: 1}}, UpgradeIDs: []string{"arsenal"}, RecruitmentPriority: []string{"vanguard", "striker", "ranger"}}
	identities := [2]battleservice.FactionIdentity{{ID: "faction-a", Name: "A", RulesetID: "castle-siege-v1", Blueprint: blueprint}, {ID: "faction-b", Name: "B", RulesetID: "castle-siege-v1", Blueprint: blueprint}}
	first, digest, err := catalog.Resolve("castle-siege-v1", identities)
	if err != nil {
		t.Fatal(err)
	}
	second, nextDigest, err := catalog.Resolve("castle-siege-v1", identities)
	if err != nil {
		t.Fatal(err)
	}
	if digest == "" || digest != nextDigest || first.Map.Width != 15 || second.Map.Height != 11 {
		t.Fatalf("unexpected resolutions: %#v", first)
	}
	if summaries := catalog.Summaries(); len(summaries) != 1 || summaries[0].RulesetID != "castle-siege-v1" || summaries[0].MapWidth != 15 || summaries[0].MaximumTicks != 500 {
		t.Fatalf("summaries = %#v", summaries)
	}
	if rulesets := catalog.Rulesets(); len(rulesets) != 1 || rulesets[0].DesignBudget != 120 {
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
	root := filepath.Join("..", "..", "..", "..")
	scenarios := t.TempDir()
	contents, err := os.ReadFile(filepath.Join(root, "scenarios", "castle-siege-v1.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	contents = append(contents, []byte("\nunknown: true\n")...)
	if err := os.WriteFile(filepath.Join(scenarios, "bad.yaml"), contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCatalog(scenarios, filepath.Join(root, "rulesets")); err == nil {
		t.Fatal("unknown scenario field accepted")
	}
}

func TestCatalogRejectsIncompatibleFaction(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..")
	catalog, err := LoadCatalog(filepath.Join(root, "scenarios"), filepath.Join(root, "rulesets"))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = catalog.Resolve("castle-siege-v1", [2]battleservice.FactionIdentity{{RulesetID: "other"}, {RulesetID: "other"}})
	if !errors.Is(err, battleservice.ErrIncompatibleFaction) {
		t.Fatalf("error = %v", err)
	}
}

func TestCatalogReturnsNotFound(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..")
	catalog, err := LoadCatalog(filepath.Join(root, "scenarios"), filepath.Join(root, "rulesets"))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = catalog.Resolve("missing", [2]battleservice.FactionIdentity{})
	if !errors.Is(err, battleservice.ErrScenarioNotFound) {
		t.Fatalf("error = %v", err)
	}
}
