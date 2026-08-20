package main

import "testing"

func TestParseOptions(t *testing.T) {
	options, err := parseOptions([]string{"-config", "engine.yaml"})
	if err != nil {
		t.Fatalf("parseOptions() returned an error: %v", err)
	}
	if options.configPath != "engine.yaml" {
		t.Fatalf("configPath = %q, want engine.yaml", options.configPath)
	}
}

func TestParseOptionsRejectsPositionals(t *testing.T) {
	if _, err := parseOptions([]string{"run"}); err == nil {
		t.Fatal("parseOptions() returned no error")
	}
}
