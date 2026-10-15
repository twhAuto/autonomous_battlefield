// Embeds shared model instructions and derives prompt versions; renders prompts and tool
// allowlists for each session purpose.

package prompts

import (
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/faction"
)

// All instructions are embedded and versioned together. The digest changes even
// if an editor forgets to bump the human-readable version.
//
//go:embed battlefield-agent.txt
var systemPromptTemplate string

//go:embed decision.txt
var decisionPromptTemplate string

//go:embed tool-context.txt
var toolContextTemplate string

//go:embed inline-context.txt
var inlineContextTemplate string

//go:embed identity.txt
var identityPromptTemplate string

//go:embed background-generation.txt
var backgroundGenerationPrompt string

//go:embed background-decision.txt
var backgroundDecisionTemplate string

var generationSystemPrompt = fmt.Sprintf(strings.TrimSpace(backgroundGenerationPrompt), faction.MaxBackgroundLength)
var GenerationVersion = generationPromptVersion(generationSystemPrompt, backgroundDecisionTemplate)

var System = strings.TrimSpace(systemPromptTemplate)
var DecisionInstructions = strings.TrimSpace(decisionPromptTemplate)

var identityTemplate = strings.TrimSpace(identityPromptTemplate)

// Version fingerprints battle prompts as assembled for a fixed sample session, so a
// change to template wording, assembly order or wiring always yields a new version.
var Version = fmt.Sprintf("battlefield-agent-v9.%x", sha256.Sum256([]byte(versionSample())))

func versionSample() string {
	session := ai.DecisionSession{RequestID: "version-sample", FactionName: "name", Background: "background", Doctrine: "doctrine"}
	return SessionSystem(session) + "\x00" + SessionDecision(session, nil) + "\x00" + SessionDecision(session, []byte("{}"))
}

func generationPromptVersion(system, decision string) string {
	return fmt.Sprintf("faction-background-v2.%x", sha256.Sum256([]byte(system+"\x00"+decision)))
}

// contextDelivery is the request-specific suffix: request ID and any inline
// battlefield context. Everything before it must stay stable across decisions
// so provider prompt caches can reuse the prefix.
func contextDelivery(requestID string, inlineContext []byte) string {
	if inlineContext == nil {
		return fmt.Sprintf(strings.TrimSpace(toolContextTemplate), requestID)
	}
	return strings.TrimSpace(inlineContextTemplate) + "\nBEGIN_BATTLEFIELD_CONTEXT\n" + string(inlineContext) + "\nEND_BATTLEFIELD_CONTEXT"
}

func SessionSystem(session ai.DecisionSession) string {
	if session.JobType == "faction_background" {
		return generationSystemPrompt
	}
	return System
}

func SessionDecision(session ai.DecisionSession, inline []byte) string {
	if session.JobType != "faction_background" {
		identity, _ := json.Marshal(map[string]string{"name": session.FactionName, "background": session.Background, "doctrine": session.Doctrine})
		// Shared policy, then per-faction identity, then per-request delivery: the
		// most stable content first maximizes the cacheable prefix.
		return DecisionInstructions + "\n" + fmt.Sprintf(identityTemplate, string(identity)) + "\n" + contextDelivery(session.RequestID, inline)
	}
	prompt := fmt.Sprintf(strings.TrimSpace(backgroundDecisionTemplate), session.RequestID)
	if inline != nil {
		prompt += "\nBEGIN_BACKGROUND_CONTEXT\n" + string(inline) + "\nEND_BACKGROUND_CONTEXT"
	}
	return prompt
}
func SessionToolNames(session ai.DecisionSession) []string {
	if session.JobType == "faction_background" {
		return []string{"get_background_context", "submit_faction_background"}
	}
	return []string{"get_decision_context", "submit_strategy_plan", "get_unit_details"}
}
