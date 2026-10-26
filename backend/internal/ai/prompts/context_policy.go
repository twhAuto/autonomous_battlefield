package prompts

import (
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

// Each removal is paired with its surviving source for review. The full mechanics
// digest is pinned: older, truncated or edited rules cannot silently lose guidance.
// Updating game content alone therefore falls back to the complete policy.
//
//go:embed rule-duplicates.json
var ruleDuplicatesJSON string

type ruleDuplicates struct {
	MechanicsSHA256 string `json:"mechanicsSHA256"`
	Rules           []struct {
		Prompt    string   `json:"prompt"`
		Mechanics []string `json:"mechanics"`
	} `json:"rules"`
}

var duplicateRules = func() ruleDuplicates {
	var rules ruleDuplicates
	if err := json.Unmarshal([]byte(ruleDuplicatesJSON), &rules); err != nil {
		panic(err) // Invalid embedded policy is a build defect, never user input.
	}
	return rules
}()

func contextPolicy(inline []byte) string {
	var envelope struct {
		Context struct {
			ContinuousProduction bool `json:"continuousProduction"`
			Knowledge            struct {
				Mechanics string `json:"mechanics"`
			} `json:"knowledge"`
		} `json:"context"`
	}
	if json.Unmarshal(inline, &envelope) != nil || !envelope.Context.ContinuousProduction ||
		fmt.Sprintf("%x", sha256.Sum256([]byte(envelope.Context.Knowledge.Mechanics))) != duplicateRules.MechanicsSHA256 {
		return DecisionInstructions
	}
	return continuousDecisionPolicy()
}

func continuousDecisionPolicy() string {
	policy := DecisionInstructions
	for _, rule := range duplicateRules.Rules {
		policy = strings.ReplaceAll(policy, rule.Prompt+" ", "")
	}
	// Only select the continuous-mode policy after reading the frozen context.
	lines := strings.Split(policy, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "Legacy recruitment (") {
			lines = append(lines[:i], lines[i+1:]...)
			break
		}
	}
	return strings.ReplaceAll(strings.Join(lines, "\n"), " (or submit recruitmentOrders in legacy mode)", "")
}
