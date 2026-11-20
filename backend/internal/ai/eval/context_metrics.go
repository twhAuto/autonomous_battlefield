package eval

import (
	"encoding/json"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/ai/agentview"
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
	payload, _ := json.Marshal(map[string]any{"context": agentview.Observation(c)})
	return len(payload)
}
