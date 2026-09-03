package ai

import (
	"strings"
	"testing"
)

func TestVersionedSystemPromptIsEmbedded(t *testing.T) {
	if strings.TrimSpace(SystemPrompt) == "" || !strings.Contains(SystemPrompt, "untrusted data") {
		t.Fatalf("versioned system prompt is empty or missing its trust boundary")
	}
	if PromptVersion != "battlefield-agent-v2" {
		t.Fatalf("PromptVersion = %q", PromptVersion)
	}
}
