// Command-line entry point for deterministic or provider AI evaluations and runtime
// provenance reports.

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/twhAuto/autonomous_battlefield/backend/gamedata"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai"
	aieval "github.com/twhAuto/autonomous_battlefield/backend/internal/ai/eval"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai/prompts"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai/providers"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("aieval", flag.ContinueOnError)
	mode := flags.String("mode", "deterministic", "deterministic, provider, compare, or metadata")
	comparisonPath := flags.String("comparison-config", "evals/comparison.yaml", "comparison variant configuration")
	repetitions := flags.Int("repetitions", 3, "matched repetitions per comparison case and variant")
	casesPath := flags.String("cases", "evals/cases", "versioned deterministic case directory")
	pricingPath := flags.String("pricing", "evals/pricing.yaml", "versioned provider pricing file")
	jsonPath := flags.String("json", "../artifacts/ai-eval/results.json", "JSON report output")
	junitPath := flags.String("junit", "../artifacts/ai-eval/results.xml", "JUnit report output")
	corpusVersion := flags.String("corpus-version", "2026-09-03.v1", "evaluation corpus version")
	providerName := flags.String("provider", "", "paid provider: openai or gemini")
	model := flags.String("model", "", "paid provider model")
	caseCount := flags.Int("case-count", 1, "number of fixed normal/adversarial cases for a paid run")
	maxSteps := flags.Int("max-steps", 4, "maximum paid provider turns")
	maxTokens := flags.Int("max-tokens", 1024, "maximum provider output tokens per turn")
	timeout := flags.Duration("timeout", 90*time.Second, "whole evaluation timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}

	if *mode == "metadata" {
		_, digest, _ := strings.Cut(prompts.Version, ".")
		content, err := gamedata.Digest()
		if err != nil {
			return fmt.Errorf("digest game content: %w", err)
		}
		fmt.Printf("%s\t%s\t%s\t%s\n", prompts.Version, digest, ai.ToolSchema, content)
		return nil
	}

	prices, err := aieval.LoadPriceBook(*pricingPath)
	if err != nil {
		return fmt.Errorf("load pricing: %w", err)
	}
	if *mode == "compare" {
		return runComparison(*comparisonPath, *casesPath, *jsonPath, *corpusVersion, prices, aieval.ComparisonOptions{Repetitions: *repetitions, MaxSteps: *maxSteps, MaxTokens: *maxTokens, Timeout: *timeout})
	}
	if *mode == "deterministic" {
		cases, err := aieval.LoadCases(*casesPath)
		if err != nil {
			return fmt.Errorf("load cases: %w", err)
		}
		report := (aieval.Runner{CorpusVersion: *corpusVersion, Prices: prices}).Run(context.Background(), cases)
		return writeReports(*jsonPath, *junitPath, report)
	}
	if *mode != "provider" {
		return fmt.Errorf("unsupported mode %q", *mode)
	}
	return runProvider(prices, providerOptions{*providerName, *model, *casesPath, *caseCount, *maxSteps, *maxTokens, *timeout, *jsonPath, *junitPath, *corpusVersion})
}

type providerOptions struct {
	provider, model, casesPath         string
	caseCount, maxSteps, maxTokens     int
	timeout                            time.Duration
	jsonPath, junitPath, corpusVersion string
}

func runProvider(prices aieval.PriceBook, options providerOptions) error {
	if os.Getenv("AI_EVAL_ALLOW_PAID") != "1" {
		return fmt.Errorf("paid evaluation requires AI_EVAL_ALLOW_PAID=1")
	}
	if options.model == "" || options.caseCount < 1 || options.caseCount > 20 || options.maxSteps < 1 || options.maxSteps > 16 || options.maxTokens < 1 || options.maxTokens > 65536 {
		return fmt.Errorf("provider mode requires bounded model, case-count, max-steps, and max-tokens")
	}
	if _, ok := prices.Estimate(options.model, ai.Usage{}); !ok {
		return fmt.Errorf("model %q has no reviewed entry in the pricing snapshot", options.model)
	}
	var turnProvider ai.TurnProvider
	switch options.provider {
	case "openai":
		if os.Getenv("OPENAI_API_KEY") == "" {
			return fmt.Errorf("OPENAI_API_KEY is required")
		}
		turnProvider = &providers.OpenAI{APIKey: os.Getenv("OPENAI_API_KEY"), Model: options.model}
	case "gemini":
		if os.Getenv("GEMINI_API_KEY") == "" {
			return fmt.Errorf("GEMINI_API_KEY is required")
		}
		turnProvider = &providers.Gemini{APIKey: os.Getenv("GEMINI_API_KEY"), Model: options.model}
	default:
		return fmt.Errorf("provider must be openai or gemini")
	}
	cases, err := aieval.LoadCases(options.casesPath)
	if err != nil {
		return fmt.Errorf("load provider cases: %w", err)
	}
	eligible := slices.DeleteFunc(cases, func(value aieval.Case) bool {
		return !value.Expected.Accepted || value.Capability != "valid" || value.ToolFailure || (value.Category != "normal" && value.Category != "adversarial")
	})
	if len(eligible) < options.caseCount {
		return fmt.Errorf("requested %d paid cases, but only %d fixed accepted normal/adversarial cases are eligible", options.caseCount, len(eligible))
	}
	eligible = eligible[:options.caseCount]
	ctx, cancel := context.WithTimeout(context.Background(), options.timeout)
	defer cancel()
	report := (aieval.Runner{CorpusVersion: options.corpusVersion, Prices: prices}).RunProvider(ctx, eligible, turnProvider, options.provider, options.model, options.maxSteps, options.maxTokens)
	return writeReports(options.jsonPath, options.junitPath, report)
}

func writeReports(jsonPath, junitPath string, report aieval.Report) error {
	if err := aieval.WriteJSON(jsonPath, report); err != nil {
		return err
	}
	if err := aieval.WriteJUnit(junitPath, report); err != nil {
		return err
	}
	if !report.Passed {
		var failed []string
		for _, value := range report.Cases {
			if !value.Passed {
				failed = append(failed, value.ID)
			}
		}
		return fmt.Errorf("AI evaluation failed: %s", strings.Join(failed, ", "))
	}
	return nil
}
