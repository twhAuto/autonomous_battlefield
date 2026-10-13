// Builds comparison runners from YAML variants and applies the command's live-provider
// limits before evaluating the corpus.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/cliisolation"
	aieval "github.com/twh09/autonomous_battlefield/backend/internal/ai/eval"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/providers"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/runner"
	"go.yaml.in/yaml/v3"
)

type comparisonConfig struct {
	KnowledgeRetrieval ai.KnowledgeLimits        `yaml:"knowledgeRetrieval"`
	Variants           []comparisonVariantConfig `yaml:"variants"`
}
type comparisonVariantConfig struct {
	WithoutKnowledge bool   `yaml:"withoutKnowledge"`
	ID               string `yaml:"id"`
	Provider         string `yaml:"provider"`
	Model            string `yaml:"model"`
	WithoutMemory    bool   `yaml:"withoutMemory"`
}

func runComparison(configPath, casesPath, jsonPath, corpus string, prices aieval.PriceBook, options aieval.ComparisonOptions) error {
	file, err := os.Open(configPath)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	config := comparisonConfig{KnowledgeRetrieval: ai.DefaultKnowledgeLimits()}
	if err := decoder.Decode(&config); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("comparison config must contain one YAML document")
	}
	if err := config.KnowledgeRetrieval.Validate(); err != nil {
		return err
	}
	options.KnowledgeRetrieval = config.KnowledgeRetrieval
	var variants []aieval.ComparisonVariant
	for _, v := range config.Variants {
		variant, err := makeComparisonVariant(v, prices)
		if err != nil {
			return err
		}
		variants = append(variants, variant)
	}
	cases, err := aieval.LoadCases(casesPath)
	if err != nil {
		return err
	}
	report, err := (aieval.Runner{CorpusVersion: corpus, Prices: prices}).Compare(context.Background(), cases, variants, options)
	if err != nil {
		return err
	}
	payload, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(jsonPath), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(jsonPath, append(payload, '\n'), 0600); err != nil {
		return err
	}
	if !report.Passed {
		return errors.New("comparison contains unsuccessful decisions; see JSON report")
	}
	return nil
}

func makeComparisonVariant(v comparisonVariantConfig, prices aieval.PriceBook) (aieval.ComparisonVariant, error) {
	result := aieval.ComparisonVariant{ID: v.ID, Provider: v.Provider, Model: v.Model, WithoutMemory: v.WithoutMemory, WithoutKnowledge: v.WithoutKnowledge, Runtime: "api"}
	if v.Provider == "scripted" {
		result.Model = "scripted"
		result.NewRunner = func() ai.DecisionRunner { return runner.APIRunner{Provider: &comparisonBaseline{}} }
		return result, nil
	}
	if os.Getenv("AI_EVAL_ALLOW_LIVE") != "1" {
		return result, errors.New("live comparison requires AI_EVAL_ALLOW_LIVE=1")
	}
	if v.Model == "" {
		return result, errors.New("live comparison requires an explicit model")
	}
	switch v.Provider {
	case "openai", "gemini":
		if os.Getenv("AI_EVAL_ALLOW_PAID") != "1" {
			return result, errors.New("API comparison requires AI_EVAL_ALLOW_PAID=1")
		}
		if _, ok := prices.Estimate(v.Model, ai.Usage{}); !ok {
			return result, fmt.Errorf("model %q has no reviewed pricing entry", v.Model)
		}
		keyName := "OPENAI_API_KEY"
		if v.Provider == "gemini" {
			keyName = "GEMINI_API_KEY"
		}
		key := os.Getenv(keyName)
		if key == "" {
			return result, fmt.Errorf("%s is required", keyName)
		}
		result.NewRunner = func() ai.DecisionRunner {
			var provider ai.TurnProvider = &providers.OpenAI{APIKey: key, Model: v.Model}
			if v.Provider == "gemini" {
				provider = &providers.Gemini{APIKey: key, Model: v.Model}
			}
			return runner.APIRunner{Provider: provider}
		}
	case cliisolation.ProviderCodex, cliisolation.ProviderAntigravity, cliisolation.ProviderClaude:
		result.Runtime = "cli"
		result.NewRunner = func() ai.DecisionRunner { return runner.CLIRunner{Provider: v.Provider, Model: v.Model} }
	default:
		return result, fmt.Errorf("unsupported comparison provider %q", v.Provider)
	}
	return result, nil
}

// The free baseline exercises the real API/MCP path using only legal context.
// It is a plumbing check, not evidence of model or memory quality.
type comparisonBaseline struct{ step int }

func (p *comparisonBaseline) NextTurn(_ context.Context, request ai.ModelTurnRequest) (ai.ModelTurnResult, error) {
	p.step++
	if p.step == 1 {
		var id string
		for _, message := range request.Messages {
			_, tail, found := strings.Cut(message.Content, "Resolve battlefield decision ")
			if found {
				id = strings.TrimSuffix(strings.Fields(tail)[0], ".")
			}
		}
		args, _ := json.Marshal(map[string]string{"requestId": id})
		return ai.ModelTurnResult{ToolCalls: []ai.ToolCall{{Name: "get_decision_context", Arguments: args}}}, nil
	}
	var envelope struct {
		Context ai.DecisionContext `json:"context"`
	}
	if json.Unmarshal([]byte(request.Messages[len(request.Messages)-1].Content), &envelope) != nil || len(envelope.Context.Legal.ActionKinds) == 0 {
		return ai.ModelTurnResult{}, errors.New("baseline requires legal context")
	}
	c := envelope.Context
	args, _ := json.Marshal(map[string]any{"requestId": c.RequestID, "idempotencyKey": "baseline", "expectedVersion": c.ExpectedVersion, "targetTick": c.TargetTick, "name": "baseline", "rationale": "Use a legal action from the visible context.", "actionWeights": map[string]int64{c.Legal.ActionKinds[0]: 1000}, "reserveGold": 0, "spendOrder": []string{}, "constructionPriorities": []any{}, "recruitmentPriorities": []string{}, "recruitmentOrders": []any{}})
	return ai.ModelTurnResult{ToolCalls: []ai.ToolCall{{Name: "submit_strategy_plan", Arguments: args}}, Usage: ai.Usage{Requests: 1}}, nil
}
