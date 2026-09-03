package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	aieval "github.com/twh09/autonomous_battlefield/backend/internal/ai/eval"
)

func TestRunDeterministicWritesPassingReports(t *testing.T) {
	directory := t.TempDir()
	root := filepath.Join("..", "..", "evals")
	jsonPath := filepath.Join(directory, "result.json")
	junitPath := filepath.Join(directory, "result.xml")
	err := run([]string{
		"-cases", filepath.Join(root, "cases"),
		"-pricing", filepath.Join(root, "pricing.yaml"),
		"-json", jsonPath,
		"-junit", junitPath,
		"-corpus-version", "test-corpus",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{jsonPath, junitPath} {
		info, statErr := os.Stat(path)
		if statErr != nil || info.Size() == 0 {
			t.Fatalf("report %s: info=%v error=%v", path, info, statErr)
		}
	}
}

func TestRunRejectsInvalidModesAndUnboundedProviderOptions(t *testing.T) {
	pricingPath := filepath.Join("..", "..", "evals", "pricing.yaml")
	t.Setenv("AI_EVAL_ALLOW_PAID", "")
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"invalid flag", []string{"-unknown"}, "flag provided but not defined"},
		{"missing pricing", []string{"-pricing", filepath.Join(t.TempDir(), "missing.yaml")}, "load pricing"},
		{"unknown mode", []string{"-pricing", pricingPath, "-mode", "unexpected"}, "unsupported mode"},
		{"paid mode not opted in", []string{"-pricing", pricingPath, "-mode", "provider"}, "AI_EVAL_ALLOW_PAID=1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := run(test.args)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("run() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestRunProviderValidatesPriceCredentialsAndProvider(t *testing.T) {
	directory := t.TempDir()
	pricingPath := filepath.Join(directory, "pricing.yaml")
	pricing := []byte("schemaVersion: battlefield.ai-pricing.v1\nversion: test\nmodels:\n  - model: paid-test\n    inputUsdPerMillion: 1\n    outputUsdPerMillion: 2\n")
	if err := os.WriteFile(pricingPath, pricing, 0o600); err != nil {
		t.Fatal(err)
	}
	base := []string{"-pricing", pricingPath, "-mode", "provider", "-model", "paid-test"}
	t.Setenv("AI_EVAL_ALLOW_PAID", "1")
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"unbounded", append(append([]string{}, base...), "-case-count", "0"), "bounded model"},
		{"unpriced", []string{"-pricing", pricingPath, "-mode", "provider", "-model", "unknown"}, "no reviewed entry"},
		{"openai credential", append(append([]string{}, base...), "-provider", "openai"), "OPENAI_API_KEY"},
		{"gemini credential", append(append([]string{}, base...), "-provider", "gemini"), "GEMINI_API_KEY"},
		{"unknown provider", append(append([]string{}, base...), "-provider", "other"), "provider must be"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("OPENAI_API_KEY", "")
			t.Setenv("GEMINI_API_KEY", "")
			err := run(test.args)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("run() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestWriteReportsReturnsEvaluationAndFilesystemErrors(t *testing.T) {
	directory := t.TempDir()
	report := aieval.Report{Passed: false, Cases: []aieval.CaseReport{{ID: "failed-case", Passed: false}}}
	err := writeReports(filepath.Join(directory, "failed.json"), filepath.Join(directory, "failed.xml"), report)
	if err == nil || !strings.Contains(err.Error(), "failed-case") {
		t.Fatalf("writeReports() error = %v", err)
	}
	if err := writeReports(directory, filepath.Join(directory, "unused.xml"), aieval.Report{Passed: true}); err == nil {
		t.Fatal("writeReports() accepted a directory as the JSON output path")
	}
	jsonPath := filepath.Join(directory, "passing.json")
	if err := writeReports(jsonPath, directory, aieval.Report{Passed: true}); err == nil {
		t.Fatal("writeReports() accepted a directory as the JUnit output path")
	}
}
