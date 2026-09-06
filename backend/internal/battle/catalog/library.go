package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"path/filepath"
	"strings"
)

type rulesetFile struct {
	game.Ruleset `yaml:",inline"`
	Library      *librarySelection `yaml:"library"`
}
type librarySelection struct {
	ID       string   `yaml:"id"`
	Version  string   `yaml:"version"`
	Units    []string `yaml:"units"`
	Upgrades []string `yaml:"upgrades"`
	Castle   string   `yaml:"castle"`
	Economy  string   `yaml:"economy"`
}
type buildingDefinition struct {
	ID      string `yaml:"id"`
	Kind    string `yaml:"kind"`
	Health  int64  `yaml:"health"`
	Defense int64  `yaml:"defense"`
	Vision  int    `yaml:"vision"`
	Income  int64  `yaml:"income"`
	Cost    int64  `yaml:"cost"`
}
type contentLibrary struct {
	ID        string               `yaml:"id"`
	Version   string               `yaml:"version"`
	Units     []game.UnitType      `yaml:"units"`
	Upgrades  []game.Upgrade       `yaml:"upgrades"`
	Buildings []buildingDefinition `yaml:"buildings"`
	Knowledge *game.RulesKnowledge `yaml:"knowledge"`
}

func resolveLibrary(directory string, source rulesetFile) (game.Ruleset, error) {
	r := source.Ruleset
	if source.Library == nil {
		return r, nil
	}
	ref := source.Library
	if r.Buildings != (game.BuildingRules{MaximumEconomyCount: r.Buildings.MaximumEconomyCount}) {
		return r, fmt.Errorf("library rulesets configure only maximumEconomyCount; building stats belong in the library")
	}
	if !safeContentName(ref.ID) || !safeContentName(ref.Version) {
		return r, fmt.Errorf("library ID and version must use letters, digits, dashes or underscores")
	}
	if len(r.Units) > 0 || len(r.Upgrades) > 0 || r.Knowledge != nil {
		return r, fmt.Errorf("library selection cannot be mixed with inline units, upgrades or knowledge")
	}
	var library contentLibrary
	path := filepath.Join(filepath.Dir(directory), "content", ref.ID+"-v"+ref.Version+".yaml")
	if err := decodeOne(path, &library); err != nil {
		return r, err
	}
	if library.ID != ref.ID || library.Version != ref.Version {
		return r, fmt.Errorf("library identity/version mismatch")
	}
	if err := validateLibrary(library); err != nil {
		return r, err
	}
	all := game.Ruleset{Units: library.Units, Upgrades: library.Upgrades, Knowledge: library.Knowledge}
	var err error
	r.Units, err = selectUnits(all.Units, ref.Units)
	if err != nil {
		return r, err
	}
	r.Upgrades, err = selectUpgrades(all.Upgrades, ref.Upgrades)
	if err != nil {
		return r, err
	}
	r.Knowledge = selectedKnowledge(library.Knowledge, r.Units)
	buildings := map[string]buildingDefinition{}
	for _, b := range library.Buildings {
		buildings[b.ID] = b
	}
	castle, ok := buildings[ref.Castle]
	if !ok || castle.Kind != "castle" {
		return r, fmt.Errorf("library castle must reference a castle building")
	}
	economy, ok := buildings[ref.Economy]
	if !ok || economy.Kind != "economy" {
		return r, fmt.Errorf("library economy must reference an economy building")
	}
	// Rulesets own limits; library definitions own building stats.
	r.Buildings = game.BuildingRules{CastleTypeID: castle.ID, EconomyTypeID: economy.ID, CastleHealth: castle.Health, CastleDefense: castle.Defense, CastleVision: castle.Vision, CastleIncome: castle.Income, EconomyCost: economy.Cost, EconomyHealth: economy.Health, EconomyDefense: economy.Defense, EconomyVision: economy.Vision, EconomyIncome: economy.Income, MaximumEconomyCount: r.Buildings.MaximumEconomyCount}
	data, _ := json.Marshal(library)
	digest := sha256.Sum256(data)
	r.ContentSource = &game.ContentSource{ID: library.ID, Version: library.Version, Digest: hex.EncodeToString(digest[:])}
	return r, nil
}
func safeContentName(value string) bool {
	if value == "" {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
func validateLibrary(l contentLibrary) error {
	// Use engine validation for every entry, including entries not selected by a ruleset.
	r := game.Ruleset{ID: l.ID, Version: l.Version, DesignBudget: 1, Units: l.Units, Upgrades: l.Upgrades, Knowledge: l.Knowledge, Buildings: game.BuildingRules{CastleHealth: 1, CastleDefense: 1, CastleVision: 1, CastleIncome: 1, EconomyCost: 1, EconomyHealth: 1, EconomyDefense: 1, EconomyVision: 1, EconomyIncome: 1, MaximumEconomyCount: 2}, Combat: game.CombatRules{MinimumDamage: 1}}
	if err := game.ValidateRuleset(r); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, b := range l.Buildings {
		if strings.TrimSpace(b.ID) == "" || seen[b.ID] || (b.Kind != "castle" && b.Kind != "economy") || b.Health <= 0 || b.Defense < 0 || b.Vision <= 0 || b.Income <= 0 || b.Cost < 0 {
			return fmt.Errorf("invalid or duplicate library building %q", b.ID)
		}
		if b.Kind == "economy" && b.Cost == 0 {
			return fmt.Errorf("economy building requires positive cost")
		}
		seen[b.ID] = true
	}
	return nil
}
