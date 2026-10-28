// Package catalog loads immutable castle-siege rulesets and scenarios.
package catalog

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"

	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
	"go.yaml.in/yaml/v3"
)

var ErrInvalidScenario = errors.New("invalid scenario")

// Directories within a catalog file system.
const (
	contentDirectory  = "content"
	rulesetDirectory  = "rulesets"
	scenarioDirectory = "scenarios"
)

type Catalog struct {
	templates map[string]template
	rulesets  map[string]game.Ruleset
}

type template struct {
	ID                    string                    `yaml:"id"`
	RulesVersion          string                    `yaml:"rulesVersion"`
	RulesetID             string                    `yaml:"rulesetId"`
	MaximumTicks          uint64                    `yaml:"maximumTicks"`
	TickDuration          string                    `yaml:"tickDuration"`
	DecisionIntervalTicks controller.Tick           `yaml:"decisionIntervalTicks"`
	Map                   game.Map                  `yaml:"map"`
	FallbackPlan          controller.PlanProposal   `yaml:"fallbackPlan"`
	ScriptedPlans         []controller.PlanProposal `yaml:"scriptedPlans"`
}

type mapFile struct {
	ID  string   `yaml:"id"`
	Map game.Map `yaml:"map"`
}

// LoadCatalog loads the scenarios, rulesets and content libraries in fsys, laid out
// as scenarios/, rulesets/ and content/ like the embedded gamedata.Files.
func LoadCatalog(fsys fs.FS) (*Catalog, error) {
	rulesets, err := loadRulesets(fsys)
	if err != nil {
		return nil, err
	}
	defaults, err := loadSharedDefaults(fsys, rulesets)
	if err != nil {
		return nil, err
	}
	entries, err := fs.ReadDir(fsys, scenarioDirectory)
	if err != nil {
		return nil, fmt.Errorf("read scenario directory: %w", err)
	}
	catalog := &Catalog{templates: make(map[string]template), rulesets: rulesets}
	for _, entry := range entries {
		if entry.IsDir() || !isYAML(entry.Name()) {
			continue
		}
		file := path.Join(scenarioDirectory, entry.Name())
		var authored mapFile
		if err := decodeOne(fsys, file, &authored); err != nil {
			return nil, err
		}
		value := defaults
		value.ID, value.Map = authored.ID, authored.Map
		if err := validateTemplate(value, rulesets); err != nil {
			return nil, fmt.Errorf("%w: scenario %q: %v", ErrInvalidScenario, file, err)
		}
		if _, exists := catalog.templates[value.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate scenario ID %q", ErrInvalidScenario, value.ID)
		}
		catalog.templates[value.ID] = value
	}
	if len(catalog.templates) == 0 {
		return nil, fmt.Errorf("%w: %s contains no YAML scenarios", ErrInvalidScenario, scenarioDirectory)
	}
	return catalog, nil
}

func (c *Catalog) IDs() []string {
	ids := make([]string, 0, len(c.templates))
	for id := range c.templates {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (c *Catalog) Summaries() []battleservice.ScenarioSummary {
	result := make([]battleservice.ScenarioSummary, 0, len(c.templates))
	for _, id := range c.IDs() {
		value := c.templates[id]
		shared := c.rulesets[value.RulesetID]
		// Summaries report the effective gold per mine, including a ruleset default.
		summaryMap := game.CloneMap(value.Map)
		summaryMap.GoldMineReserve = game.GoldMineReserveFor(value.Map, shared.Buildings)
		result = append(result, battleservice.ScenarioSummary{AllowedUnits: unitIDs(shared), AllowedUpgrades: upgradeIDs(shared), AllowedBuildings: buildingIDs(shared), ID: value.ID, RulesVersion: value.RulesVersion, RulesetID: value.RulesetID, MaximumTicks: value.MaximumTicks, TickDuration: value.TickDuration, DecisionIntervalTicks: value.DecisionIntervalTicks, MapWidth: value.Map.Width, MapHeight: value.Map.Height, Map: summaryMap})
	}
	return result
}

func (c *Catalog) Rulesets() []game.Ruleset {
	ids := make([]string, 0, len(c.rulesets))
	for id := range c.rulesets {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]game.Ruleset, 0, len(ids))
	for _, id := range ids {
		result = append(result, cloneRuleset(c.rulesets[id]))
	}
	return result
}

func (c *Catalog) Ruleset(id string) (game.Ruleset, bool) {
	value, ok := c.rulesets[id]
	return cloneRuleset(value), ok
}

// Resolve assembles a validated scenario for two factions. The battle service attaches
// controllers and computes the battle's digest with battleservice.ScenarioDigest.
func (c *Catalog) Resolve(id string, identities [2]battleservice.FactionIdentity) (game.Scenario, error) {
	tmpl, ok := c.templates[id]
	if !ok {
		return game.Scenario{}, fmt.Errorf("%w: %q", battleservice.ErrScenarioNotFound, id)
	}
	ruleset := c.rulesets[tmpl.RulesetID]
	factions := make([]game.FactionSetup, 2)
	for index, identity := range identities {

		factions[index] = game.FactionSetup{ID: identity.ID, Name: identity.Name, Doctrine: identity.Doctrine, Background: identity.Background, FallbackPlan: controller.ClonePlanProposal(tmpl.FallbackPlan), ScriptedPlans: controller.ClonePlanProposals(tmpl.ScriptedPlans)}
	}
	// validateTemplateTiming already parsed every loaded template's duration.
	duration, _ := time.ParseDuration(tmpl.TickDuration)
	resolved := game.Scenario{SnapshotVersion: game.DoctrineSnapshotVersion, ID: tmpl.ID, RulesVersion: tmpl.RulesVersion, RulesetID: tmpl.RulesetID, MaximumTicks: tmpl.MaximumTicks, TickDuration: duration, DecisionIntervalTicks: tmpl.DecisionIntervalTicks, Map: game.CloneMap(tmpl.Map), Ruleset: cloneRuleset(ruleset), Factions: factions}
	if _, err := game.NewMatch("scenario-validation", resolved, 1); err != nil {
		return game.Scenario{}, fmt.Errorf("%w: scenario %q: %v", ErrInvalidScenario, id, err)
	}
	return resolved, nil
}

func loadRulesets(fsys fs.FS) (map[string]game.Ruleset, error) {
	entries, err := fs.ReadDir(fsys, rulesetDirectory)
	if err != nil {
		return nil, fmt.Errorf("read ruleset directory: %w", err)
	}
	result := make(map[string]game.Ruleset)
	for _, entry := range entries {
		if entry.IsDir() || !isYAML(entry.Name()) {
			continue
		}
		file := path.Join(rulesetDirectory, entry.Name())
		var source rulesetFile
		if err := decodeOne(fsys, file, &source); err != nil {
			return nil, err
		}
		value, err := resolveLibrary(fsys, source)
		if err != nil {
			return nil, fmt.Errorf("ruleset %q: %w", file, err)
		}
		if err := game.ValidateRuleset(value); err != nil {
			return nil, fmt.Errorf("ruleset %q: %w", file, err)
		}
		if _, exists := result[value.ID]; exists {
			return nil, fmt.Errorf("duplicate ruleset ID %q", value.ID)
		}
		result[value.ID] = value
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("%s contains no YAML rulesets", rulesetDirectory)
	}
	return result, nil
}

func validateTemplate(value template, rulesets map[string]game.Ruleset) error {
	if strings.TrimSpace(value.ID) == "" || strings.TrimSpace(value.RulesVersion) == "" || strings.TrimSpace(value.RulesetID) == "" {
		return errors.New("ID, rules version, and ruleset ID are required")
	}
	ruleset, ok := rulesets[value.RulesetID]
	if !ok {
		return fmt.Errorf("unknown ruleset %q", value.RulesetID)
	}
	// Battles and the API report the scenario's rules version, so it must name the
	// version of the ruleset it actually uses.
	if value.RulesVersion != ruleset.Version {
		return fmt.Errorf("rules version %q does not match ruleset %q version %q", value.RulesVersion, value.RulesetID, ruleset.Version)
	}
	if err := validateSelectedPlans(value, ruleset); err != nil {
		return err
	}
	if err := validateTemplateTiming(value); err != nil {
		return err
	}
	if err := game.ValidateMap(value.Map); err != nil {
		return err
	}
	if strings.TrimSpace(value.FallbackPlan.Name) == "" || len(value.ScriptedPlans) == 0 {
		return errors.New("fallback and scripted plans are required")
	}
	for _, plan := range append([]controller.PlanProposal{value.FallbackPlan}, value.ScriptedPlans...) {
		if err := controller.ValidatePlanProposal(plan, []string{game.ActionAttack, game.ActionDefend}); err != nil {
			return err
		}
	}
	return nil
}

// decodeOne strictly decodes the single YAML document at name in fsys.
func decodeOne(fsys fs.FS, name string, destination any) error {
	file, err := fsys.Open(name)
	if err != nil {
		return fmt.Errorf("open catalog file %q: %w", name, err)
	}
	defer file.Close()
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode catalog file %q: %w", name, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("catalog file %q must contain one YAML document", name)
		}
		return fmt.Errorf("decode trailing catalog content %q: %w", name, err)
	}
	return nil
}

func isYAML(name string) bool {
	extension := strings.ToLower(path.Ext(name))
	return extension == ".yaml" || extension == ".yml"
}

func cloneRuleset(value game.Ruleset) game.Ruleset {
	value.Buildings.CastleTrains = append([]string(nil), value.Buildings.CastleTrains...)
	value.Knowledge = game.CloneKnowledge(value.Knowledge)
	if value.ContentSource != nil {
		copy := *value.ContentSource
		value.ContentSource = &copy
	}
	value.Structures = game.CloneBuildingTypes(value.Structures)
	value.Units = append([]game.UnitType(nil), value.Units...)
	value.Upgrades = append([]game.Upgrade{}, value.Upgrades...)
	return value
}

func validateTemplateTiming(value template) error {
	if value.MaximumTicks == 0 || value.MaximumTicks > 10_000 {
		return errors.New("maximum ticks must be between 1 and 10000")
	}
	duration, err := time.ParseDuration(value.TickDuration)
	if err != nil || duration < 0 || duration > 10*time.Second {
		return errors.New("tick duration must be between 0 and 10s")
	}
	if value.DecisionIntervalTicks == 0 {
		return errors.New("decision interval must be positive")
	}
	return nil
}

func loadSharedDefaults(fsys fs.FS, rulesets map[string]game.Ruleset) (template, error) {
	if len(rulesets) != 1 {
		return template{}, fmt.Errorf("exactly one shared ruleset is required")
	}
	var defaults template
	if err := decodeOne(fsys, "battlefield.yaml", &defaults); err != nil {
		return template{}, err
	}
	if defaults.ID != "" || defaults.Map.Width != 0 || defaults.RulesVersion != "" {
		return template{}, fmt.Errorf("battlefield config must not define a map or rules version")
	}
	ruleset, ok := rulesets[defaults.RulesetID]
	if !ok {
		return template{}, fmt.Errorf("unknown shared ruleset %q", defaults.RulesetID)
	}
	defaults.RulesVersion = ruleset.Version
	return defaults, nil
}
