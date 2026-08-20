package catalog

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/server/service"
)

func TestCatalogLoadsAndResolvesScenario(t *testing.T) {
	catalog, err := LoadCatalog(filepath.Join("..", "..", "..", "..", "scenarios"))
	if err != nil {
		t.Fatalf("LoadCatalog() returned an error: %v", err)
	}
	identities := [2]battleservice.FactionIdentity{{ID: "faction-a", Name: "A"}, {ID: "faction-b", Name: "B"}}
	first, firstDigest, err := catalog.Resolve("skirmish-v1", identities)
	if err != nil {
		t.Fatalf("Resolve() returned an error: %v", err)
	}
	second, secondDigest, err := catalog.Resolve("skirmish-v1", identities)
	if err != nil {
		t.Fatalf("second Resolve() returned an error: %v", err)
	}
	if firstDigest == "" || firstDigest != secondDigest || first.Factions[0].ID != "faction-a" || second.Factions[1].Name != "B" {
		t.Fatalf("resolved scenarios/digests differ: %q and %q", firstDigest, secondDigest)
	}
	if first.Factions[0].Traits[0].ID != "aggressive" {
		t.Fatalf("first trait = %#v, want aggressive", first.Factions[0].Traits)
	}
	summaries := catalog.Summaries()
	if len(summaries) != 1 || summaries[0].ID != "skirmish-v1" || summaries[0].DecisionIntervalTicks != 10 {
		t.Fatalf("summaries = %#v", summaries)
	}
}

func TestCatalogRejectsUnknownFields(t *testing.T) {
	directory := t.TempDir()
	contents := `
id: bad
rulesVersion: "1"
tickDuration: 0s
decisionIntervalTicks: 10
decisionLeadTicks: 2
unknown: true
`
	if err := os.WriteFile(filepath.Join(directory, "bad.yaml"), []byte(contents), 0o600); err != nil {
		t.Fatalf("write scenario: %v", err)
	}
	if _, err := LoadCatalog(directory); err == nil {
		t.Fatal("LoadCatalog() returned no error")
	}
}

func TestCatalogReturnsNotFound(t *testing.T) {
	catalog, err := LoadCatalog(filepath.Join("..", "..", "..", "..", "scenarios"))
	if err != nil {
		t.Fatalf("LoadCatalog() returned an error: %v", err)
	}
	_, _, err = catalog.Resolve("missing", [2]battleservice.FactionIdentity{})
	if !errors.Is(err, battleservice.ErrScenarioNotFound) {
		t.Fatalf("Resolve() error = %v, want ErrScenarioNotFound", err)
	}
}

func TestCatalogRejectsInvalidStatsAndPlansAtStartup(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "scenarios", "skirmish-v1.yaml"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	tests := []struct {
		name    string
		oldText string
		newText string
	}{
		{name: "oversized unit stat", oldText: "health: 42", newText: "health: 1000000001"},
		{name: "unknown plan action", oldText: "attack: 1400", newText: "retreat: 1400"},
		{name: "invalid unused trait", oldText: "defend: 350", newText: "retreat: 350"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			directory := t.TempDir()
			contents := strings.Replace(string(fixture), tt.oldText, tt.newText, 1)
			if err := os.WriteFile(filepath.Join(directory, "invalid.yaml"), []byte(contents), 0o600); err != nil {
				t.Fatalf("write scenario: %v", err)
			}
			if _, err := LoadCatalog(directory); !errors.Is(err, ErrInvalidScenario) {
				t.Fatalf("LoadCatalog() error = %v, want ErrInvalidScenario", err)
			}
		})
	}
}

func TestCatalogRejectsDuplicateScenarioAndUnitIDs(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "scenarios", "skirmish-v1.yaml"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	t.Run("scenario ID", func(t *testing.T) {
		directory := t.TempDir()
		for _, name := range []string{"one.yaml", "two.yaml"} {
			if err := os.WriteFile(filepath.Join(directory, name), fixture, 0o600); err != nil {
				t.Fatalf("write scenario: %v", err)
			}
		}
		if _, err := LoadCatalog(directory); !errors.Is(err, ErrInvalidScenario) {
			t.Fatalf("LoadCatalog() error = %v, want ErrInvalidScenario", err)
		}
	})
	t.Run("resolved unit ID", func(t *testing.T) {
		directory := t.TempDir()
		contents := strings.Replace(string(fixture), "id: striker", "id: vanguard", 1)
		if err := os.WriteFile(filepath.Join(directory, "duplicate.yaml"), []byte(contents), 0o600); err != nil {
			t.Fatalf("write scenario: %v", err)
		}
		if _, err := LoadCatalog(directory); !errors.Is(err, ErrInvalidScenario) {
			t.Fatalf("LoadCatalog() error = %v, want ErrInvalidScenario", err)
		}
	})
}
