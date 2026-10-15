// Checks the knowledge retrieval limits accept the defaults and reject non-positive bounds.

package ai

import "testing"

func TestKnowledgeLimitsValidate(t *testing.T) {
	if err := DefaultKnowledgeLimits().Validate(); err != nil {
		t.Fatalf("default limits rejected: %v", err)
	}
	for _, limits := range []KnowledgeLimits{{}, {MaxUnitCards: 1}, {MaxBytes: 1}, {MaxUnitCards: -1, MaxBytes: 1}} {
		if limits.Validate() == nil {
			t.Fatalf("limits %+v accepted", limits)
		}
	}
}
