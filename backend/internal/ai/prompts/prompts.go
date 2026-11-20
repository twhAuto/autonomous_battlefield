// Embeds shared model instructions and derives prompt versions; renders prompts and tool
// allowlists for each session purpose.

package prompts

import (
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai/agentview"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/faction"
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

//go:embed inline-blocks.txt
var inlineBlocksTemplate string

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
var Version = fmt.Sprintf("battlefield-agent-v11.%x", sha256.Sum256([]byte(versionSample())))

const versionContextSample = `{"context":{"schemaVersion":"v4","encoding":"tables-v1","continuousProduction":true,"knowledge":{"mechanics":"sample"},"legal":{"actionKinds":[],"units":[],"buildings":[],"upgrades":[],"recruitmentSources":[],"economySites":[],"constructionRuns":[],"goldMineSites":[]},"view":{"map":{},"unitProfiles":[],"buildingProfiles":[],"unitMap":{},"state":{}},"factionName":"name","background":"background","doctrine":"doctrine","battleId":"battle","factionId":"faction","ownedUpgradeIds":[],"economy":{},"activePlan":{},"memory":[],"memoryEconomyShared":{},"referenceUnits":[],"futureField":null,"requestId":"version-sample","targetTick":1,"expectedVersion":2,"observationVersion":3,"observationHash":"hash","opening":false}}`

func versionSample() string {
	session := ai.DecisionSession{RequestID: "version-sample", FactionName: "name", Background: "background", Doctrine: "doctrine"}
	// Exercise both delivery layouts and all ordered leaves. Policy selection data
	// is included too, so changing a rule removal cannot reuse old provenance.
	inline := []byte(versionContextSample)
	return SessionSystem(session) + "\x00" + SessionDecision(session, nil) + "\x00" + SessionDecision(session, []byte("{}")) + "\x00" + SessionDecision(session, inline) + "\x00" + continuousDecisionPolicy() + "\x00" + ruleDuplicatesJSON
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
		if reference, state, ok := agentview.ContextBlocks(inline); ok {
			return contextPolicy(inline) + "\n" + strings.TrimSpace(inlineBlocksTemplate) +
				"\nBEGIN_BATTLEFIELD_REFERENCE\n" + string(reference) + "\nEND_BATTLEFIELD_REFERENCE\n" +
				fmt.Sprintf(identityTemplate, string(identity)) +
				"\nBEGIN_BATTLEFIELD_CONTEXT\n" + string(state) + "\nEND_BATTLEFIELD_CONTEXT"
		}
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

// SessionDecisionSharedBytes is the length of the leading part of
// SessionDecision(session, nil) that every battle decision shares: the decision
// instructions, before the faction identity and request.
func SessionDecisionSharedBytes(session ai.DecisionSession) int {
	if session.JobType == "faction_background" {
		return 0
	}
	return len(DecisionInstructions) + 1
}

func SessionToolNames(session ai.DecisionSession) []string {
	if session.JobType == "faction_background" {
		return []string{"get_background_context", "submit_faction_background"}
	}
	return []string{"get_decision_context", "submit_strategy_plan", "get_unit_details"}
}
