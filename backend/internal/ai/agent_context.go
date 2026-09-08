// Projects frozen observations into bounded agent responses and serves exact unit references.
package ai

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"unicode/utf8"

	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
)

type UnitOverview struct {
	ID         string `json:"id"`
	Cost       int64  `json:"cost"`
	Class      string `json:"class,omitempty"`
	Role       string `json:"role,omitempty"`
	Strengths  string `json:"strengths,omitempty"`
	Weaknesses string `json:"weaknesses,omitempty"`
}

type ReferenceIdentity struct {
	RulesetID      string `json:"rulesetId"`
	RulesetVersion string `json:"rulesetVersion"`
	Digest         string `json:"digest"`
}

type UnitReferenceSnapshot struct {
	ReferenceIdentity
	Units []UnitDetail `json:"units"`
}

type UnitDetail struct {
	UnitID string         `json:"unitId"`
	Stats  game.UnitType  `json:"stats"`
	Card   *game.UnitCard `json:"card,omitempty"`
}

type AgentKnowledge struct {
	ReferenceIdentity
	Mechanics string `json:"mechanics,omitempty"`
}

type AgentLegalChoices struct {
	Buildings          []game.BuildingType     `json:"buildings,omitempty"`
	RecruitmentSources []string                `json:"recruitmentSources,omitempty"`
	Upgrades           []game.Upgrade          `json:"upgrades,omitempty"`
	ActionKinds        []string                `json:"actionKinds"`
	Units              []UnitOverview          `json:"units"`
	EconomySites       []controller.Coordinate `json:"economySites"`
}

// Deliberately does not embed DecisionContext: new stored fields are private by default.
type AgentContext struct {
	FactionName        string                  `json:"factionName,omitempty"`
	Background         string                  `json:"background,omitempty"`
	Doctrine           string                  `json:"doctrine,omitempty"`
	Opening            bool                    `json:"opening,omitempty"`
	OwnedUpgradeIDs    []string                `json:"ownedUpgradeIds,omitempty"`
	SchemaVersion      string                  `json:"schemaVersion"`
	RequestID          string                  `json:"requestId"`
	BattleID           string                  `json:"battleId"`
	FactionID          string                  `json:"factionId"`
	TargetTick         uint64                  `json:"targetTick"`
	ExpectedVersion    int64                   `json:"expectedVersion"`
	ObservationVersion uint64                  `json:"observationVersion"`
	ObservationHash    string                  `json:"observationHash"`
	View               game.View               `json:"view"`
	Economy            EconomyContext          `json:"economy"`
	Memory             []FactionMemoryEntry    `json:"memory,omitempty"`
	ActivePlan         controller.PlanProposal `json:"activePlan"`
	Legal              AgentLegalChoices       `json:"legal"`
	Knowledge          *AgentKnowledge         `json:"knowledge,omitempty"`
	ReferenceUnits     []UnitOverview          `json:"referenceUnits,omitempty"`
}

// FreezeUnitReference captures all and only legal or visible types, independently of
// the legacy automatic knowledge budget. Values are copied from the frozen ruleset.
func FreezeUnitReference(r game.Ruleset, c DecisionContext) *UnitReferenceSnapshot {
	payload, _ := json.Marshal(r)
	digest := sha256.Sum256(payload)
	result := &UnitReferenceSnapshot{ReferenceIdentity: ReferenceIdentity{r.ID, r.Version, hex.EncodeToString(digest[:])}, Units: []UnitDetail{}}
	allowed := visibleAndLegalUnitIDs(c)
	cards := map[string]game.UnitCard{}
	if r.Knowledge != nil {
		for _, card := range r.Knowledge.Cards {
			cards[card.UnitID] = card
		}
	}
	for _, stats := range r.Units {
		if !allowed[stats.ID] {
			continue
		}
		detail := UnitDetail{UnitID: stats.ID, Stats: stats}
		if card, ok := cards[stats.ID]; ok {
			detail.Card = &card
		}
		result.Units = append(result.Units, detail)
	}
	sort.Slice(result.Units, func(i, j int) bool { return result.Units[i].UnitID < result.Units[j].UnitID })
	return result
}

func visibleAndLegalUnitIDs(c DecisionContext) map[string]bool {
	ids := map[string]bool{}
	for _, unit := range c.Legal.Units {
		ids[unit.ID] = true
	}
	for _, unit := range c.View.State.Units {
		ids[unit.Type] = true
	}
	return ids
}

func unitOverview(stats game.UnitType, card *game.UnitCard) UnitOverview {
	result := UnitOverview{ID: stats.ID, Cost: stats.Cost, Class: stats.Class}
	if card != nil {
		result.Role = card.Role
		result.Strengths = card.Strengths
		result.Weaknesses = card.Weaknesses
	}
	return result
}

func CompactContext(c DecisionContext) AgentContext {
	result := AgentContext{
		FactionName: c.FactionName, Background: c.Background, Doctrine: c.Doctrine,
		Opening: c.Opening, OwnedUpgradeIDs: c.OwnedUpgradeIDs, SchemaVersion: c.SchemaVersion,
		RequestID: c.RequestID, BattleID: c.BattleID, FactionID: c.FactionID,
		TargetTick: c.TargetTick, ExpectedVersion: c.ExpectedVersion,
		ObservationVersion: c.ObservationVersion, ObservationHash: c.ObservationHash,
		View: c.View, Economy: c.Economy, Memory: c.Memory, ActivePlan: c.ActivePlan,
		Legal: AgentLegalChoices{
			Buildings: c.Legal.Buildings, RecruitmentSources: c.Legal.RecruitmentSources,
			Upgrades: c.Legal.Upgrades, ActionKinds: c.Legal.ActionKinds,
			EconomySites: c.Legal.EconomySites, Units: []UnitOverview{},
		},
	}

	details := map[string]UnitDetail{}
	if c.UnitReference != nil {
		result.Knowledge = &AgentKnowledge{ReferenceIdentity: c.UnitReference.ReferenceIdentity}
		for _, detail := range c.UnitReference.Units {
			details[detail.UnitID] = detail
		}
	}
	if c.Knowledge != nil {
		if result.Knowledge == nil {
			result.Knowledge = &AgentKnowledge{ReferenceIdentity: ReferenceIdentity{c.Knowledge.RulesetID, c.Knowledge.RulesetVersion, c.Knowledge.Digest}}
		}
		result.Knowledge.Mechanics = c.Knowledge.Mechanics
	}
	legal := map[string]bool{}
	for _, unit := range c.Legal.Units {
		legal[unit.ID] = true
		result.Legal.Units = append(result.Legal.Units, unitOverview(unit, details[unit.ID].Card))
	}
	visible := visibleAndLegalUnitIDs(c)
	for id, detail := range details {
		if visible[id] && !legal[id] {
			result.ReferenceUnits = append(result.ReferenceUnits, unitOverview(detail.Stats, detail.Card))
		}
	}
	sort.Slice(result.ReferenceUnits, func(i, j int) bool { return result.ReferenceUnits[i].ID < result.ReferenceUnits[j].ID })
	return result
}

// Legacy decisions retain their original wire shape and are never synthesized from live data.
func AgentObservation(c DecisionContext) any {
	if c.SchemaVersion == ContextSchema {
		return CompactContext(c)
	}
	c.UnitReference = nil
	return c
}

// UnitDetailsMaxBytes leaves headroom below the observed CLI offload threshold.
const UnitDetailsMaxBytes = 1800

type UnitDetailResult struct {
	GuidanceOffset     int            `json:"guidanceOffset,omitempty"`
	NextGuidanceOffset *int           `json:"nextGuidanceOffset,omitempty"`
	UnitID             string         `json:"unitId"`
	Available          bool           `json:"available"`
	Stats              *game.UnitType `json:"stats,omitempty"`
	Guidance           string         `json:"guidance,omitempty"`
}
type UnitDetailsResponse struct {
	ReferenceIdentity
	Units []UnitDetailResult `json:"units"`
}

func LookupUnitDetails(c DecisionContext, ids []string) (UnitDetailsResponse, error) {
	return LookupUnitDetailsPage(c, ids, 0)
}

// Guidance offsets are UTF-8 byte offsets into the frozen card. A continuation
// repeats base stats and never truncates guidance without an explicit next offset.
func LookupUnitDetailsPage(c DecisionContext, ids []string, offset int) (UnitDetailsResponse, error) {
	response := UnitDetailsResponse{Units: []UnitDetailResult{}}
	if offset < 0 || (offset > 0 && len(ids) != 1) {
		return response, fmt.Errorf("guidanceOffset requires one unit ID and a nonnegative offset")
	}
	if len(ids) < 1 || len(ids) > 4 {
		return response, fmt.Errorf("supply 1–4 distinct unit IDs")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || seen[id] {
			return response, fmt.Errorf("supply 1–4 distinct nonempty unit IDs")
		}
		seen[id] = true
	}
	details := map[string]UnitDetail{}
	if c.UnitReference != nil {
		response.ReferenceIdentity = c.UnitReference.ReferenceIdentity
		for _, detail := range c.UnitReference.Units {
			details[detail.UnitID] = detail
		}
	} else {
		// Legacy records may only have some cards, but legal base stats remain authoritative.
		if c.Knowledge != nil {
			response.ReferenceIdentity = ReferenceIdentity{c.Knowledge.RulesetID, c.Knowledge.RulesetVersion, c.Knowledge.Digest}
			for _, card := range c.Knowledge.Cards {
				copy := card.UnitCard
				details[card.UnitID] = UnitDetail{UnitID: card.UnitID, Stats: card.Stats, Card: &copy}
			}
		}
		for _, unit := range c.Legal.Units {
			detail := details[unit.ID]
			detail.UnitID = unit.ID
			detail.Stats = unit
			details[unit.ID] = detail
		}
	}
	allowed := visibleAndLegalUnitIDs(c)
	for _, id := range ids {
		result := UnitDetailResult{UnitID: id}
		if detail, ok := details[id]; ok && allowed[id] {
			stats := detail.Stats
			result.Available = true
			result.Stats = &stats
			if detail.Card != nil {
				guidance := detail.Card.Guidance
				if offset > len(guidance) || (offset < len(guidance) && !utf8.RuneStart(guidance[offset])) {
					return response, fmt.Errorf("invalid guidanceOffset")
				}
				result.GuidanceOffset = offset
				result.Guidance = guidance[offset:]
			}
		}
		response.Units = append(response.Units, result)
	}
	payload, _ := json.Marshal(response)
	if len(payload) > UnitDetailsMaxBytes {
		if len(ids) > 1 {
			return UnitDetailsResponse{}, fmt.Errorf("unit details exceed %d bytes; request one unit ID at a time", UnitDetailsMaxBytes)
		}
		unit := &response.Units[0]
		remaining := unit.Guidance
		// Trim only complete UTF-8 runes; nextGuidanceOffset makes every byte retrievable.
		for len(payload) > UnitDetailsMaxBytes && len(unit.Guidance) > 0 {
			_, width := utf8.DecodeLastRuneInString(unit.Guidance)
			unit.Guidance = unit.Guidance[:len(unit.Guidance)-width]
			next := offset + len(unit.Guidance)
			unit.NextGuidanceOffset = &next
			payload, _ = json.Marshal(response)
		}
		if len(payload) > UnitDetailsMaxBytes || (remaining != "" && unit.Guidance == "") {
			return UnitDetailsResponse{}, fmt.Errorf("unit stats exceed inline response budget")
		}
	}
	return response, nil
}
