package ai

import (
	_ "embed"
	"strings"
)

//go:embed prompts/battlefield-agent-v2.txt
var systemPromptTemplate string

// SystemPrompt is loaded from the versioned prompt template included in the
// binary. PromptVersion is persisted beside every decision and evaluation.
var SystemPrompt = strings.TrimSpace(systemPromptTemplate)
