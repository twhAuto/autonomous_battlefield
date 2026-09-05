package ai

import (
	"crypto/sha256"
	_ "embed"
	"fmt"
	"strings"
)

// All instructions are embedded and versioned together. The digest changes even
// if an editor forgets to bump the human-readable version.
//
//go:embed prompts/battlefield-agent-v3.txt
var systemPromptTemplate string

//go:embed prompts/decision-v3.txt
var decisionPromptTemplate string

//go:embed prompts/tool-context-v3.txt
var toolContextTemplate string

//go:embed prompts/inline-context-v3.txt
var inlineContextTemplate string

var SystemPrompt = strings.TrimSpace(systemPromptTemplate)
var DecisionInstructions = strings.TrimSpace(decisionPromptTemplate)
var PromptVersion = fmt.Sprintf("battlefield-agent-v3.%x", sha256.Sum256([]byte(systemPromptTemplate+"\x00"+decisionPromptTemplate+"\x00"+toolContextTemplate+"\x00"+inlineContextTemplate)))

// DecisionPrompt varies only the context transport; decision policy is shared.
// A nil context means the model must fetch it through MCP.
func DecisionPrompt(requestID string, inlineContext []byte) string {
	delivery := fmt.Sprintf(strings.TrimSpace(toolContextTemplate), requestID)
	if inlineContext != nil {
		delivery = strings.TrimSpace(inlineContextTemplate)
	}
	prompt := DecisionInstructions + "\n" + delivery
	if inlineContext != nil {
		prompt += "\nBEGIN_BATTLEFIELD_CONTEXT\n" + string(inlineContext) + "\nEND_BATTLEFIELD_CONTEXT"
	}
	return prompt
}
