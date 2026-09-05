package ai

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"sort"
)

type KnowledgeLimits struct {
	MaxUnitCards int `json:"maxUnitCards" yaml:"maxUnitCards"`
	MaxBytes     int `json:"maxBytes" yaml:"maxBytes"`
}

func DefaultKnowledgeLimits() KnowledgeLimits { return KnowledgeLimits{16, 16 << 10} }
func (l KnowledgeLimits) Validate() error {
	if l.MaxUnitCards <= 0 || l.MaxBytes <= 0 {
		return fmt.Errorf("knowledgeRetrieval limits must be positive")
	}
	return nil
}

type RetrievedCard struct {
	game.UnitCard
	Stats game.UnitType `json:"stats"`
}
type DecisionKnowledge struct {
	RulesetID      string          `json:"rulesetId"`
	RulesetVersion string          `json:"rulesetVersion"`
	Digest         string          `json:"digest"`
	Limits         KnowledgeLimits `json:"limits"`
	Mechanics      string          `json:"mechanics,omitempty"`
	Cards          []RetrievedCard `json:"cards"`
	SelectedIDs    []string        `json:"selectedIds"`
	SerializedSize int             `json:"serializedSize"`
	Truncated      bool            `json:"truncated"`
}

// Size includes metadata and its own decimal representation.
func knowledgeSize(k *DecisionKnowledge) int {
	for {
		b, _ := json.Marshal(k)
		if len(b) == k.SerializedSize {
			return len(b)
		}
		k.SerializedSize = len(b)
	}
}
func RetrieveKnowledge(r game.Ruleset, c DecisionContext, limits KnowledgeLimits) (*DecisionKnowledge, error) {
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	if r.Knowledge == nil {
		return nil, nil
	}
	payload, _ := json.Marshal(r)
	digest := sha256.Sum256(payload)
	k := &DecisionKnowledge{RulesetID: r.ID, RulesetVersion: r.Version, Digest: hex.EncodeToString(digest[:]), Limits: limits, Cards: []RetrievedCard{}, SelectedIDs: []string{}}
	// Very small budgets cannot accommodate even metadata. Keep authoritative context usable.
	k.Truncated = true
	if knowledgeSize(k) > limits.MaxBytes {
		return nil, nil
	}
	k.Mechanics = r.Knowledge.Mechanics
	if knowledgeSize(k) > limits.MaxBytes {
		k.Mechanics = ""
	}
	cards := map[string]game.UnitCard{}
	for _, card := range r.Knowledge.Cards {
		cards[card.UnitID] = card
	}
	units := game.UnitTypesByID(r)
	ids := knowledgeCandidates(c)
	for _, id := range ids {
		card, ok := cards[id]
		if !ok {
			continue
		}
		if len(k.Cards) >= limits.MaxUnitCards {
			break
		}
		k.Cards = append(k.Cards, RetrievedCard{card, units[id]})
		k.SelectedIDs = append(k.SelectedIDs, id)
		if knowledgeSize(k) > limits.MaxBytes {
			k.Cards = k.Cards[:len(k.Cards)-1]
			k.SelectedIDs = k.SelectedIDs[:len(k.SelectedIDs)-1]
			break
		}
	}
	k.Truncated = len(k.Cards) < len(ids) || k.Mechanics == ""
	if knowledgeSize(k) > limits.MaxBytes {
		k.Truncated = true
		knowledgeSize(k)
	}
	return k, nil
}
func knowledgeCandidates(c DecisionContext) []string {
	enemy, own := []string{}, []string{}
	for _, u := range c.View.State.Units {
		if u.FactionID == c.FactionID {
			own = append(own, u.Type)
		} else {
			enemy = append(enemy, u.Type)
		}
	}
	sort.Strings(enemy)
	sort.Strings(own)
	legal := append([]game.UnitType(nil), c.Legal.Units...)
	sort.Slice(legal, func(i, j int) bool {
		if legal[i].Cost != legal[j].Cost {
			return legal[i].Cost < legal[j].Cost
		}
		return legal[i].ID < legal[j].ID
	})
	ids := append(enemy, own...)
	if c.ActivePlan.RecruitmentOrders != nil {
		for _, order := range *c.ActivePlan.RecruitmentOrders {
			ids = append(ids, order.UnitType)
		}
	}
	ids = append(ids, c.ActivePlan.RecruitmentPriorities...)
	for _, u := range legal {
		ids = append(ids, u.ID)
	}
	result := []string{}
	seen := map[string]bool{}
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			result = append(result, id)
		}
	}
	return result
}
