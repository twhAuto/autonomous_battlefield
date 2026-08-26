// Package catalog loads and resolves versioned battle scenario templates.
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

	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/server/service"
	battle "github.com/twh09/autonomous_battlefield/backend/internal/battle/simulation"
	"github.com/twh09/autonomous_battlefield/backend/internal/battle/simulation/controller"
	"go.yaml.in/yaml/v3"
)

var ErrInvalidScenario = errors.New("invalid scenario")

type Catalog struct {
	templates map[string]template
}

type template struct {
	ID                    string                     `yaml:"id"`
	RulesVersion          string                     `yaml:"rulesVersion"`
	TickDuration          string                     `yaml:"tickDuration"`
	DecisionIntervalTicks controller.Tick            `yaml:"decisionIntervalTicks"`
	DecisionLeadTicks     controller.Tick            `yaml:"decisionLeadTicks"`
	Rules                 battle.CombatRules         `yaml:"rules"`
	TraitDefinitions      map[string]traitDefinition `yaml:"traitDefinitions"`
	Slots                 []slot                     `yaml:"slots"`
}

type traitDefinition struct {
	ActionWeightModifiers map[string]int64 `yaml:"actionWeightModifiers"`
}

type slot struct {
	Units         []battle.UnitTemplate     `yaml:"units"`
	Traits        []string                  `yaml:"traits"`
	FallbackPlan  controller.PlanProposal   `yaml:"fallbackPlan"`
	ScriptedPlans []controller.PlanProposal `yaml:"scriptedPlans"`
}

func LoadCatalog(directory string) (*Catalog, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("read scenario directory %q: %w", directory, err)
	}

	catalog := &Catalog{templates: make(map[string]template)}
	loaded := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		extension := strings.ToLower(filepath.Ext(entry.Name()))
		if extension != ".yaml" && extension != ".yml" {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		tmpl, err := loadTemplate(path)
		if err != nil {
			return nil, err
		}
		if _, duplicate := catalog.templates[tmpl.ID]; duplicate {
			return nil, fmt.Errorf("%w: duplicate scenario ID %q", ErrInvalidScenario, tmpl.ID)
		}
		catalog.templates[tmpl.ID] = tmpl
		loaded++
	}
	if loaded == 0 {
		return nil, fmt.Errorf("%w: directory %q contains no YAML scenarios", ErrInvalidScenario, directory)
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
	ids := c.IDs()
	summaries := make([]battleservice.ScenarioSummary, 0, len(ids))
	for _, id := range ids {
		tmpl := c.templates[id]
		summaries = append(summaries, battleservice.ScenarioSummary{
			ID:                    tmpl.ID,
			RulesVersion:          tmpl.RulesVersion,
			TickDuration:          tmpl.TickDuration,
			DecisionIntervalTicks: tmpl.DecisionIntervalTicks,
			DecisionLeadTicks:     tmpl.DecisionLeadTicks,
		})
	}
	return summaries
}

func (c *Catalog) Resolve(id string, identities [2]battleservice.FactionIdentity) (battle.Scenario, string, error) {
	tmpl, ok := c.templates[id]
	if !ok {
		return battle.Scenario{}, "", fmt.Errorf("%w: %q", battleservice.ErrScenarioNotFound, id)
	}
	resolved, err := resolveTemplate(tmpl, identities)
	if err != nil {
		return battle.Scenario{}, "", fmt.Errorf("%w: scenario %q: %v", ErrInvalidScenario, id, err)
	}
	if _, err := battle.NewBattle("scenario-validation", resolved, 1); err != nil {
		return battle.Scenario{}, "", fmt.Errorf("%w: scenario %q: %v", ErrInvalidScenario, id, err)
	}
	payload, err := json.Marshal(resolved)
	if err != nil {
		return battle.Scenario{}, "", fmt.Errorf("marshal resolved scenario %q: %w", id, err)
	}
	digest := sha256.Sum256(payload)
	return resolved, hex.EncodeToString(digest[:]), nil
}

func loadTemplate(path string) (template, error) {
	file, err := os.Open(path)
	if err != nil {
		return template{}, fmt.Errorf("open scenario %q: %w", path, err)
	}
	defer file.Close()

	var tmpl template
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(&tmpl); err != nil {
		return template{}, fmt.Errorf("decode scenario %q: %w", path, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return template{}, fmt.Errorf("%w: scenario %q must contain one YAML document", ErrInvalidScenario, path)
		}
		return template{}, fmt.Errorf("decode trailing scenario content %q: %w", path, err)
	}
	if err := validateTemplate(tmpl); err != nil {
		return template{}, fmt.Errorf("%w: scenario %q: %v", ErrInvalidScenario, path, err)
	}
	return tmpl, nil
}

func validateTemplate(tmpl template) error {
	if err := validateTemplateMetadata(tmpl); err != nil {
		return err
	}
	if err := validateTraitDefinitions(tmpl); err != nil {
		return err
	}
	if err := validateSlots(tmpl); err != nil {
		return err
	}
	resolved, err := resolveTemplate(tmpl, [2]battleservice.FactionIdentity{{ID: "validation-a", Name: "Validation A"}, {ID: "validation-b", Name: "Validation B"}})
	if err != nil {
		return err
	}
	if _, err := battle.NewBattle("scenario-validation", resolved, 1); err != nil {
		return err
	}
	return nil
}

func validateTemplateMetadata(tmpl template) error {
	if strings.TrimSpace(tmpl.ID) == "" || strings.TrimSpace(tmpl.RulesVersion) == "" {
		return errors.New("ID and rules version are required")
	}
	duration, err := time.ParseDuration(tmpl.TickDuration)
	if err != nil || duration < 0 || duration > 10*time.Second {
		return errors.New("tick duration must be between 0 and 10s")
	}
	if tmpl.DecisionIntervalTicks == 0 || tmpl.DecisionLeadTicks == 0 || tmpl.DecisionLeadTicks >= tmpl.DecisionIntervalTicks {
		return errors.New("decision lead must be positive and less than the interval")
	}
	if tmpl.Rules.MinimumDamage <= 0 || tmpl.Rules.DefendAttackPercent < 0 || tmpl.Rules.DefendAttackPercent > 100 || tmpl.Rules.DefendDamageTakenPercent < 0 || tmpl.Rules.DefendDamageTakenPercent > 100 {
		return errors.New("combat percentages must be in [0, 100] and minimum damage must be positive")
	}
	if len(tmpl.Slots) != 2 {
		return errors.New("exactly two slots are required")
	}
	return nil
}

func validateTraitDefinitions(tmpl template) error {
	for traitID, definition := range tmpl.TraitDefinitions {
		if strings.TrimSpace(traitID) == "" {
			return errors.New("trait IDs must not be empty")
		}
		_, err := controller.NewUtilityController(controller.Config{
			DecisionIntervalTicks: tmpl.DecisionIntervalTicks,
			DecisionLeadTicks:     tmpl.DecisionLeadTicks,
			FirstDecisionTick:     1,
			DefaultActionWeight:   1000,
			ActionKinds:           []string{battle.ActionAttack, battle.ActionDefend},
			FallbackPlan:          controller.PlanProposal{Name: "validation"},
			Traits: []controller.Trait{{
				ID:                    traitID,
				ActionWeightModifiers: cloneMap(definition.ActionWeightModifiers),
			}},
		})
		if err != nil {
			return fmt.Errorf("trait %q: %v", traitID, err)
		}
	}
	return nil
}

func validateSlots(tmpl template) error {
	for slotIndex, slot := range tmpl.Slots {
		if len(slot.Units) == 0 || len(slot.ScriptedPlans) == 0 || strings.TrimSpace(slot.FallbackPlan.Name) == "" {
			return fmt.Errorf("slot %d requires units, a fallback plan, and scripted plans", slotIndex)
		}
		for _, traitID := range slot.Traits {
			if _, exists := tmpl.TraitDefinitions[traitID]; !exists {
				return fmt.Errorf("slot %d refers to undefined trait %q", slotIndex, traitID)
			}
		}
	}
	return nil
}

func resolveTemplate(tmpl template, identities [2]battleservice.FactionIdentity) (battle.Scenario, error) {
	duration, err := time.ParseDuration(tmpl.TickDuration)
	if err != nil {
		return battle.Scenario{}, fmt.Errorf("tick duration: %v", err)
	}
	resolved := battle.Scenario{
		ID:                    tmpl.ID,
		RulesVersion:          tmpl.RulesVersion,
		TickDuration:          duration,
		DecisionIntervalTicks: tmpl.DecisionIntervalTicks,
		DecisionLeadTicks:     tmpl.DecisionLeadTicks,
		Rules:                 tmpl.Rules,
		Factions:              make([]battle.FactionSetup, 2),
	}
	for index, identity := range identities {
		selectedTraits := make([]controller.Trait, 0, len(tmpl.Slots[index].Traits))
		for _, traitID := range tmpl.Slots[index].Traits {
			definition := tmpl.TraitDefinitions[traitID]
			selectedTraits = append(selectedTraits, controller.Trait{
				ID:                    traitID,
				ActionWeightModifiers: cloneMap(definition.ActionWeightModifiers),
			})
		}
		resolved.Factions[index] = battle.FactionSetup{
			ID:            identity.ID,
			Name:          identity.Name,
			Units:         append([]battle.UnitTemplate(nil), tmpl.Slots[index].Units...),
			Traits:        selectedTraits,
			FallbackPlan:  clonePlan(tmpl.Slots[index].FallbackPlan),
			ScriptedPlans: clonePlans(tmpl.Slots[index].ScriptedPlans),
		}
	}
	return resolved, nil
}

func cloneMap(values map[string]int64) map[string]int64 {
	copy := make(map[string]int64, len(values))
	for key, value := range values {
		copy[key] = value
	}
	return copy
}

func clonePlan(plan controller.PlanProposal) controller.PlanProposal {
	return controller.PlanProposal{Name: plan.Name, ActionWeights: cloneMap(plan.ActionWeights), TargetBonuses: cloneMap(plan.TargetBonuses)}
}

func clonePlans(plans []controller.PlanProposal) []controller.PlanProposal {
	copy := make([]controller.PlanProposal, len(plans))
	for index, plan := range plans {
		copy[index] = clonePlan(plan)
	}
	return copy
}
