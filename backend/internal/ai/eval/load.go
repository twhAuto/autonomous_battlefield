// Loads and validates versioned evaluation cases and model prices from YAML before
// execution.

package eval

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

func LoadCases(root string) ([]Case, error) {
	var cases []Case
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || (filepath.Ext(path) != ".yaml" && filepath.Ext(path) != ".yml") {
			return nil
		}
		value, err := loadCase(path)
		if err != nil {
			return err
		}
		cases = append(cases, value)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(cases) == 0 {
		return nil, errors.New("evaluation corpus is empty")
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].ID < cases[j].ID })
	for i := 1; i < len(cases); i++ {
		if cases[i-1].ID == cases[i].ID {
			return nil, fmt.Errorf("duplicate case ID %q", cases[i].ID)
		}
	}
	return cases, nil
}

func loadCase(path string) (Case, error) {
	file, err := os.Open(path)
	if err != nil {
		return Case{}, err
	}
	defer file.Close()
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	var value Case
	if err := decoder.Decode(&value); err != nil {
		return Case{}, fmt.Errorf("decode %s: %w", path, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Case{}, fmt.Errorf("%s must contain exactly one YAML document", path)
	}
	if err := validateCase(value); err != nil {
		return Case{}, fmt.Errorf("validate %s: %w", path, err)
	}
	return value, nil
}

func LoadPriceBook(path string) (PriceBook, error) {
	file, err := os.Open(path)
	if err != nil {
		return PriceBook{}, err
	}
	defer file.Close()
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	var value PriceBook
	if err := decoder.Decode(&value); err != nil {
		return PriceBook{}, err
	}
	if strings.TrimSpace(value.SchemaVersion) == "" || strings.TrimSpace(value.Version) == "" {
		return PriceBook{}, errors.New("pricing schemaVersion and version are required")
	}
	return value, nil
}

func validateCase(value Case) error {
	if value.SchemaVersion != CaseSchema {
		return fmt.Errorf("schemaVersion must be %q", CaseSchema)
	}
	if strings.TrimSpace(value.ID) == "" || strings.TrimSpace(value.Category) == "" || strings.TrimSpace(value.RequestID) == "" {
		return errors.New("id, category, and requestId are required")
	}
	if value.MaxSteps < 1 || value.MaxSteps > 16 || value.MaxTokens < 1 || value.MaxTokens > 65536 {
		return errors.New("maxSteps or maxTokens is outside the evaluation safety bound")
	}
	if value.Capability != "valid" && value.Capability != "expired" && value.Capability != "tampered" {
		return errors.New("capability must be valid, expired, or tampered")
	}
	if value.Expected.MaximumToolCalls < 0 || value.Expected.MaximumToolCalls > 32 {
		return errors.New("maximumToolCalls is outside the evaluation safety bound")
	}
	return nil
}
