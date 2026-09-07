// Summarizes retrieved knowledge in evaluation reports without including the full decision
// context.

package eval

import "github.com/twh09/autonomous_battlefield/backend/internal/ai"

type KnowledgeReport struct {
	Digest         string             `json:"digest"`
	Limits         ai.KnowledgeLimits `json:"limits"`
	SelectedIDs    []string           `json:"selectedIds"`
	SerializedSize int                `json:"serializedSize"`
	Truncated      bool               `json:"truncated"`
}

func knowledgeReport(k *ai.DecisionKnowledge) *KnowledgeReport {
	if k == nil {
		return nil
	}
	return &KnowledgeReport{k.Digest, k.Limits, append([]string(nil), k.SelectedIDs...), k.SerializedSize, k.Truncated}
}
