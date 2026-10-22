// Resolves versioned content libraries into rulesets, validating source references and
// hashing loaded library content.

package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/twh09/autonomous_battlefield/backend/internal/game"
)

type rulesetFile struct {
	game.Ruleset `yaml:",inline"`
	Library      *librarySelection `yaml:"library"`
}
type librarySelection struct {
	ID      string `yaml:"id"`
	Version string `yaml:"version"`
	Castle  string `yaml:"castle"`
	Economy string `yaml:"economy"`
}

// Keep the original JSON field names in library hashes for unchanged v1 content.
type buildingDefinition struct {
	Trains     []string `json:"Trains,omitempty" yaml:"trains,omitempty"`
	BuildRange int      `json:"BuildRange,omitempty" yaml:"buildRange,omitempty"`
	ID         string   `yaml:"id"`
	Kind       string   `yaml:"kind"`
	Health     int64    `yaml:"health"`
	Defense    int64    `yaml:"defense"`
	Vision     int      `yaml:"vision"`
	Income     int64    `yaml:"income"`
	Cost       int64    `yaml:"cost"`
	Attack     int64    `json:"Attack,omitempty" yaml:"attack,omitempty"`
	Range      int      `json:"Range,omitempty" yaml:"range,omitempty"`
}

func (b buildingDefinition) buildingType() game.BuildingType {
	return game.BuildingType{Trains: append([]string(nil), b.Trains...), BuildRange: b.BuildRange, ID: b.ID, Kind: b.Kind, Health: b.Health, Defense: b.Defense, Vision: b.Vision, Income: b.Income, Cost: b.Cost, Attack: b.Attack, Range: b.Range}
}

type contentLibrary struct {
	ID        string               `yaml:"id"`
	Version   string               `yaml:"version"`
	Units     []game.UnitType      `yaml:"units"`
	Upgrades  []game.Upgrade       `yaml:"upgrades"`
	Buildings []buildingDefinition `yaml:"buildings"`
	Knowledge *game.RulesKnowledge `yaml:"knowledge"`
}

func resolveLibrary(fsys fs.FS, source rulesetFile) (game.Ruleset, error) {
	r := source.Ruleset
	if source.Library == nil {
		return r, nil
	}
	ref := source.Library
	if r.Buildings != (game.BuildingRules{MaximumEconomyCount: r.Buildings.MaximumEconomyCount, MaximumBuildingCount: r.Buildings.MaximumBuildingCount}) {
		return r, fmt.Errorf("library rulesets configure only building limits; building stats belong in the library")
	}
	if !safeContentName(ref.ID) || !safeContentName(ref.Version) {
		return r, fmt.Errorf("library ID and version must use letters, digits, dashes or underscores")
	}
	if len(r.Units) > 0 || len(r.Upgrades) > 0 || len(r.Structures) > 0 || r.Knowledge != nil {
		return r, fmt.Errorf("library selection cannot be mixed with inline units, upgrades, structures or knowledge")
	}
	var library contentLibrary
	if err := decodeOne(fsys, path.Join(contentDirectory, ref.ID+"-v"+ref.Version+".yaml"), &library); err != nil {
		return r, err
	}
	if library.ID != ref.ID || library.Version != ref.Version {
		return r, fmt.Errorf("library identity/version mismatch")
	}
	if err := validateLibrary(library); err != nil {
		return r, err
	}
	r.Units = append([]game.UnitType(nil), library.Units...)
	r.Upgrades = append([]game.Upgrade(nil), library.Upgrades...)
	r.Knowledge = game.CloneKnowledge(library.Knowledge)
	var err error
	r, err = applyLibraryBuildings(r, library, ref)
	if err != nil {
		return r, err
	}
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
		if strings.TrimSpace(b.ID) == "" || seen[b.ID] || !validLibraryBuilding(b) {
			return fmt.Errorf("invalid or duplicate library building %q", b.ID)
		}
		if b.Kind == "economy" && b.Cost == 0 {
			return fmt.Errorf("economy building requires positive cost")
		}
		seen[b.ID] = true
	}
	return nil
}

func validLibraryBuilding(b buildingDefinition) bool {
	if b.Kind != "castle" && b.Kind != "economy" {
		return game.ValidStructure(b.buildingType())
	}
	return len(b.Trains) == 0 && b.Health > 0 && b.Defense >= 0 && b.Vision >= 0 && b.BuildRange >= 0 && b.BuildRange <= 10 && b.Income > 0 && b.Cost >= 0 && b.Attack == 0 && b.Range == 0
}

func applyLibraryBuildings(r game.Ruleset, library contentLibrary, ref *librarySelection) (game.Ruleset, error) {
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
	r.Buildings = game.BuildingRules{CastleBuildRange: castle.BuildRange, EconomyBuildRange: economy.BuildRange, CastleTypeID: castle.ID, EconomyTypeID: economy.ID, CastleHealth: castle.Health, CastleDefense: castle.Defense, CastleVision: castle.Vision, CastleIncome: castle.Income, EconomyCost: economy.Cost, EconomyHealth: economy.Health, EconomyDefense: economy.Defense, EconomyVision: economy.Vision, EconomyIncome: economy.Income, MaximumEconomyCount: r.Buildings.MaximumEconomyCount, MaximumBuildingCount: r.Buildings.MaximumBuildingCount}
	for _, b := range library.Buildings {
		if b.Kind != "castle" && b.Kind != "economy" {
			r.Structures = append(r.Structures, b.buildingType())
		}
	}
	return r, nil
}
