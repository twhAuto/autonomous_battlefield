package ai

import (
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

// All instructions are embedded and versioned together. The digest changes even
// if an editor forgets to bump the human-readable version.
//
//go:embed prompts/battlefield-agent-v4.txt
var systemPromptTemplate string

//go:embed prompts/decision-v4.txt
var decisionPromptTemplate string

//go:embed prompts/tool-context-v4.txt
var toolContextTemplate string

//go:embed prompts/inline-context-v4.txt
var inlineContextTemplate string

//go:embed prompts/doctrine-v4.txt
var doctrinePromptTemplate string

var SystemPrompt = strings.TrimSpace(systemPromptTemplate)
var DecisionInstructions = strings.TrimSpace(decisionPromptTemplate)
var PromptVersion = fmt.Sprintf("battlefield-agent-v4.%x", sha256.Sum256([]byte(systemPromptTemplate+"\x00"+doctrinePromptTemplate+"\x00"+decisionPromptTemplate+"\x00"+toolContextTemplate+"\x00"+inlineContextTemplate)))

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

// Doctrine is separate from untrusted battlefield observations and subordinate to core rules.
func SystemPromptForDoctrine(doctrine string) string {
	if strings.TrimSpace(doctrine) == "" {
		return SystemPrompt
	}
	encoded, _ := json.Marshal(doctrine)
	return SystemPrompt + "\n" + fmt.Sprintf(strings.TrimSpace(doctrinePromptTemplate), string(encoded))
}
