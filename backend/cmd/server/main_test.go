package main

import "testing"

func TestParseOptionsDefaultsToStartingServer(t *testing.T) {
	options, err := parseOptions(nil)
	if err != nil {
		t.Fatalf("parseOptions() returned an error: %v", err)
	}

	if options.configPath != "config.yaml" {
		t.Fatalf("configPath = %q, want config.yaml", options.configPath)
	}
	if options.migrate {
		t.Fatal("migrate = true, want false")
	}
}

func TestParseOptionsAcceptsMigrationMode(t *testing.T) {
	options, err := parseOptions([]string{"-migrate", "-config", "testdata/config.yaml"})
	if err != nil {
		t.Fatalf("parseOptions() returned an error: %v", err)
	}

	if options.configPath != "testdata/config.yaml" {
		t.Fatalf("configPath = %q, want testdata/config.yaml", options.configPath)
	}
	if !options.migrate {
		t.Fatal("migrate = false, want true")
	}
}

func TestParseOptionsRejectsPositionalArguments(t *testing.T) {
	if _, err := parseOptions([]string{"up"}); err == nil {
		t.Fatal("parseOptions() returned no error")
	}
}
