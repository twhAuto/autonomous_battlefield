package agentview

import (
	"encoding/json"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai"
)

// Omit exact identity copies already supplied in the prompt's faction identity. Never
// summarize user-authored identity or change the canonical inspector response.
func SessionContextPayload(session ai.DecisionSession, payload json.RawMessage) json.RawMessage {
	if session.JobType == "faction_background" {
		return payload
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(payload, &envelope) != nil {
		return payload
	}
	var observation map[string]json.RawMessage
	if json.Unmarshal(envelope["context"], &observation) != nil || observation == nil {
		return payload
	}
	changed := false
	for key, value := range map[string]string{"factionName": session.FactionName, "background": session.Background, "doctrine": session.Doctrine} {
		if value != "" && sameContextJSON(observation[key], contextJSON(value)) {
			delete(observation, key)
			changed = true
		}
	}
	if !changed {
		return payload
	}
	envelope["context"] = orderedContext(observation)
	return contextJSON(envelope)
}
