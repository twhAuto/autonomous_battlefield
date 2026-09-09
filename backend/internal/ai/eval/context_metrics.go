package eval

import (
	"encoding/json"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
)

func countUnitDetailCalls(calls []string) int {
	n := 0
	for _, call := range calls {
		if call == "get_unit_details" {
			n++
		}
	}
	return n
}
func evaluationContextBytes(value Case) int {
	c := evaluationObservation(value)
	payload, _ := json.Marshal(map[string]any{"context": ai.AgentObservation(c)})
	return len(payload)
}
