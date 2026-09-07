// Embeds shared model instructions and derives prompt versions; renders prompts and tool
// allowlists for each session purpose.

package ai

import (
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/twh09/autonomous_battlefield/backend/internal/faction"
)

// All instructions are embedded and versioned together. The digest changes even
// if an editor forgets to bump the human-readable version.
//
//go:embed prompts/battlefield-agent.txt
var systemPromptTemplate string

//go:embed prompts/decision.txt
var decisionPromptTemplate string

//go:embed prompts/tool-context.txt
var toolContextTemplate string

//go:embed prompts/inline-context.txt
var inlineContextTemplate string

//go:embed prompts/doctrine.txt
var doctrinePromptTemplate string

//go:embed prompts/identity.txt
var identityPromptTemplate string

//go:embed prompts/background-generation.txt
var backgroundGenerationPrompt string

//go:embed prompts/background-decision.txt
var backgroundDecisionTemplate string

var generationSystemPrompt = fmt.Sprintf(strings.TrimSpace(backgroundGenerationPrompt), faction.MaxBackgroundLength)
var GenerationPromptVersion = generationPromptVersion(generationSystemPrompt, backgroundDecisionTemplate)

var SystemPrompt = strings.TrimSpace(systemPromptTemplate)
var DecisionInstructions = strings.TrimSpace(decisionPromptTemplate)
var PromptVersion = fmt.Sprintf("battlefield-agent-v5.%x", sha256.Sum256([]byte(systemPromptTemplate+"\x00"+identityPromptTemplate+"\x00"+doctrinePromptTemplate+"\x00"+decisionPromptTemplate+"\x00"+toolContextTemplate+"\x00"+inlineContextTemplate)))

func generationPromptVersion(system, decision string) string {
	return fmt.Sprintf("faction-background-v2.%x", sha256.Sum256([]byte(system+"\x00"+decision)))
}

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

func SessionSystemPrompt(session DecisionSession) string {
	if session.JobType == "faction_background" {
		return generationSystemPrompt
	}
	if session.FactionName == "" && session.Background == "" {
		return SystemPromptForDoctrine(session.Doctrine)
	}
	value, _ := json.Marshal(map[string]string{"name": session.FactionName, "background": session.Background, "doctrine": session.Doctrine})
	return SystemPrompt + "\n" + fmt.Sprintf(strings.TrimSpace(identityPromptTemplate), string(value))
}
func SessionDecisionPrompt(session DecisionSession, inline []byte) string {
	if session.JobType != "faction_background" {
		return DecisionPrompt(session.RequestID, inline)
	}
	prompt := fmt.Sprintf(strings.TrimSpace(backgroundDecisionTemplate), session.RequestID)
	if inline != nil {
		prompt += "\nBEGIN_BACKGROUND_CONTEXT\n" + string(inline) + "\nEND_BACKGROUND_CONTEXT"
	}
	return prompt
}
func SessionToolNames(session DecisionSession) []string {
	if session.JobType == "faction_background" {
		return []string{"get_background_context", "submit_faction_background"}
	}
	return []string{"get_decision_context", "submit_strategy_plan"}
}
