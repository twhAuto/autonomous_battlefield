// Package catalog loads immutable castle-siege rulesets and scenarios.
package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
	"go.yaml.in/yaml/v3"
)

var ErrInvalidScenario = errors.New("invalid scenario")

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

// LoadCatalog loads scenarios and rulesets. Without the optional path, the
// rulesets directory beside the scenario directory is used.
func LoadCatalog(scenarioDirectory string, rulesetDirectory ...string) (*Catalog, error) {
	rulesPath := filepath.Join(filepath.Dir(scenarioDirectory), "rulesets")
	if len(rulesetDirectory) > 0 {
		rulesPath = rulesetDirectory[0]
	}
	rulesets, err := loadRulesets(rulesPath)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(scenarioDirectory)
	if err != nil {
		return nil, fmt.Errorf("read scenario directory %q: %w", scenarioDirectory, err)
	}
	catalog := &Catalog{templates: make(map[string]template), rulesets: rulesets}
	for _, entry := range entries {
		if entry.IsDir() || !isYAML(entry.Name()) {
			continue
		}
		path := filepath.Join(scenarioDirectory, entry.Name())
		var value template
		if err := decodeOne(path, &value); err != nil {
			return nil, err
		}
		if err := validateTemplate(value, rulesets); err != nil {
			return nil, fmt.Errorf("%w: scenario %q: %v", ErrInvalidScenario, path, err)
		}
		if _, exists := catalog.templates[value.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate scenario ID %q", ErrInvalidScenario, value.ID)
		}
		catalog.templates[value.ID] = value
	}
	if len(catalog.templates) == 0 {
		return nil, fmt.Errorf("%w: directory %q contains no YAML scenarios", ErrInvalidScenario, scenarioDirectory)
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
		result = append(result, battleservice.ScenarioSummary{ID: value.ID, RulesVersion: value.RulesVersion, RulesetID: value.RulesetID, MaximumTicks: value.MaximumTicks, TickDuration: value.TickDuration, DecisionIntervalTicks: value.DecisionIntervalTicks, MapWidth: value.Map.Width, MapHeight: value.Map.Height})
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

func (c *Catalog) Resolve(id string, identities [2]battleservice.FactionIdentity) (game.Scenario, string, error) {
	tmpl, ok := c.templates[id]
	if !ok {
		return game.Scenario{}, "", fmt.Errorf("%w: %q", battleservice.ErrScenarioNotFound, id)
	}
	ruleset := c.rulesets[tmpl.RulesetID]
	factions := make([]game.FactionSetup, 2)
	for index, identity := range identities {
		if identity.RulesetID != tmpl.RulesetID {
			return game.Scenario{}, "", fmt.Errorf("%w: faction %q uses ruleset %q, scenario requires %q", battleservice.ErrIncompatibleFaction, identity.ID, identity.RulesetID, tmpl.RulesetID)
		}
		if _, err := game.ValidateBlueprint(ruleset, identity.Blueprint); err != nil {
			return game.Scenario{}, "", err
		}
		factions[index] = game.FactionSetup{ID: identity.ID, Name: identity.Name, Blueprint: game.CloneBlueprint(identity.Blueprint), FallbackPlan: clonePlan(tmpl.FallbackPlan), ScriptedPlans: clonePlans(tmpl.ScriptedPlans)}
	}
	duration, _ := time.ParseDuration(tmpl.TickDuration)
	resolved := game.Scenario{ID: tmpl.ID, RulesVersion: tmpl.RulesVersion, RulesetID: tmpl.RulesetID, MaximumTicks: tmpl.MaximumTicks, TickDuration: duration, DecisionIntervalTicks: tmpl.DecisionIntervalTicks, Map: game.CloneMap(tmpl.Map), Ruleset: cloneRuleset(ruleset), Factions: factions}
	if _, err := game.NewMatch("scenario-validation", resolved, 1); err != nil {
		return game.Scenario{}, "", fmt.Errorf("%w: scenario %q: %v", ErrInvalidScenario, id, err)
	}
	payload, err := json.Marshal(resolved)
	if err != nil {
		return game.Scenario{}, "", fmt.Errorf("marshal resolved scenario %q: %w", id, err)
	}
	digest := sha256.Sum256(payload)
	return resolved, hex.EncodeToString(digest[:]), nil
}

func loadRulesets(directory string) (map[string]game.Ruleset, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("read ruleset directory %q: %w", directory, err)
	}
	result := make(map[string]game.Ruleset)
	for _, entry := range entries {
		if entry.IsDir() || !isYAML(entry.Name()) {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		var value game.Ruleset
		if err := decodeOne(path, &value); err != nil {
			return nil, err
		}
		if err := game.ValidateRuleset(value); err != nil {
			return nil, fmt.Errorf("ruleset %q: %w", path, err)
		}
		if _, exists := result[value.ID]; exists {
			return nil, fmt.Errorf("duplicate ruleset ID %q", value.ID)
		}
		result[value.ID] = value
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("directory %q contains no YAML rulesets", directory)
	}
	return result, nil
}

func validateTemplate(value template, rulesets map[string]game.Ruleset) error {
	if strings.TrimSpace(value.ID) == "" || strings.TrimSpace(value.RulesVersion) == "" || strings.TrimSpace(value.RulesetID) == "" {
		return errors.New("ID, rules version, and ruleset ID are required")
	}
	if _, ok := rulesets[value.RulesetID]; !ok {
		return fmt.Errorf("unknown ruleset %q", value.RulesetID)
	}
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

func decodeOne(path string, destination any) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open catalog file %q: %w", path, err)
	}
	defer file.Close()
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode catalog file %q: %w", path, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("catalog file %q must contain one YAML document", path)
		}
		return fmt.Errorf("decode trailing catalog content %q: %w", path, err)
	}
	return nil
}

func isYAML(name string) bool {
	extension := strings.ToLower(filepath.Ext(name))
	return extension == ".yaml" || extension == ".yml"
}

func cloneRuleset(value game.Ruleset) game.Ruleset {
	value.Units = append([]game.UnitType(nil), value.Units...)
	value.Upgrades = append([]game.Upgrade(nil), value.Upgrades...)
	return value
}

func clonePlan(value controller.PlanProposal) controller.PlanProposal {
	result := controller.PlanProposal{Name: value.Name, Rationale: value.Rationale, ActionWeights: make(map[string]int64, len(value.ActionWeights)), TargetBonuses: make(map[string]int64, len(value.TargetBonuses)), ReserveGold: value.ReserveGold,
		SpendOrder: append([]string(nil), value.SpendOrder...), ConstructionPriorities: append([]controller.Coordinate(nil), value.ConstructionPriorities...), RecruitmentPriorities: append([]string(nil), value.RecruitmentPriorities...)}
	for key, weight := range value.ActionWeights {
		result.ActionWeights[key] = weight
	}
	for key, bonus := range value.TargetBonuses {
		result.TargetBonuses[key] = bonus
	}
	return result
}

func clonePlans(values []controller.PlanProposal) []controller.PlanProposal {
	result := make([]controller.PlanProposal, len(values))
	for index := range values {
		result[index] = clonePlan(values[index])
	}
	return result
}
