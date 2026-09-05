package config

import (
	"go.yaml.in/yaml/v3"
	"testing"
)

func TestKnowledgeConfiguration(t *testing.T) {
	for _, tc := range []struct {
		text         string
		count, bytes int
		invalid      bool
	}{
		{"", 16, 16384, false}, {"engine: {knowledgeRetrieval: {maxUnitCards: 32, maxBytes: 32768}}", 32, 32768, false}, {"engine: {knowledgeRetrieval: {maxUnitCards: 4}}", 4, 16384, false}, {"engine: {knowledgeRetrieval: {maxUnitCards: 0}}", 0, 0, true}, {"engine: {knowledgeRetrieval: {maxBytes: -1}}", 0, 0, true},
	} {
		var raw fileConfig
		if err := yaml.Unmarshal([]byte(tc.text), &raw); err != nil {
			t.Fatal(err)
		}
		c, err := parseEngineConfig(raw)
		if tc.invalid {
			if err == nil {
				t.Fatal("accepted invalid config")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if c.KnowledgeRetrieval.MaxUnitCards != tc.count || c.KnowledgeRetrieval.MaxBytes != tc.bytes {
			t.Fatalf("limits: %+v", c.KnowledgeRetrieval)
		}
	}
}
