// Projects frozen observations into bounded agent responses and serves exact unit references.
package agentview

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"unicode/utf8"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
)

type UnitOverview struct {
	TrainingTicks uint64 `json:"trainingTicks"`
	ID            string `json:"id"`
	Cost          int64  `json:"cost"`
	Class         string `json:"class,omitempty"`
	Role          string `json:"role,omitempty"`
	Strengths     string `json:"strengths,omitempty"`
	Weaknesses    string `json:"weaknesses,omitempty"`
}

type Knowledge struct {
	ai.ReferenceIdentity
	Mechanics string `json:"mechanics,omitempty"`
}

type LegalChoices struct {
	Buildings          []game.BuildingType     `json:"buildings,omitempty"`
	RecruitmentSources []string                `json:"recruitmentSources,omitempty"`
	Upgrades           []game.Upgrade          `json:"upgrades,omitempty"`
	ActionKinds        []string                `json:"actionKinds"`
	Units              []UnitOverview          `json:"units"`
	EconomySites       []controller.Coordinate `json:"economySites"`
}

// Deliberately does not embed DecisionContext: new stored fields are private by default.
type Context struct {
	ContinuousProduction bool                    `json:"continuousProduction,omitempty"`
	FactionName          string                  `json:"factionName,omitempty"`
	Background           string                  `json:"background,omitempty"`
	Doctrine             string                  `json:"doctrine,omitempty"`
	Opening              bool                    `json:"opening,omitempty"`
	OwnedUpgradeIDs      []string                `json:"ownedUpgradeIds,omitempty"`
	SchemaVersion        string                  `json:"schemaVersion"`
	RequestID            string                  `json:"requestId"`
	BattleID             string                  `json:"battleId"`
	FactionID            string                  `json:"factionId"`
	TargetTick           uint64                  `json:"targetTick"`
	ExpectedVersion      int64                   `json:"expectedVersion"`
	ObservationVersion   uint64                  `json:"observationVersion"`
	ObservationHash      string                  `json:"observationHash"`
	View                 game.View               `json:"view"`
	Economy              ai.EconomyContext       `json:"economy"`
	Memory               []ai.FactionMemoryEntry `json:"memory,omitempty"`
	ActivePlan           controller.PlanProposal `json:"activePlan"`
	Legal                LegalChoices            `json:"legal"`
	Knowledge            *Knowledge              `json:"knowledge,omitempty"`
	ReferenceUnits       []UnitOverview          `json:"referenceUnits,omitempty"`
}

// FreezeUnitReference captures all and only legal or visible types, independently of
// the legacy automatic knowledge budget. Values are copied from the frozen ruleset.
func FreezeUnitReference(r game.Ruleset, c ai.DecisionContext) *ai.UnitReferenceSnapshot {
	payload, _ := json.Marshal(r)
	digest := sha256.Sum256(payload)
	result := &ai.UnitReferenceSnapshot{ReferenceIdentity: ai.ReferenceIdentity{RulesetID: r.ID, RulesetVersion: r.Version, Digest: hex.EncodeToString(digest[:])}, Units: []ai.UnitDetail{}}
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
		detail := ai.UnitDetail{UnitID: stats.ID, Stats: stats}
		if card, ok := cards[stats.ID]; ok {
			detail.Card = &card
		}
		result.Units = append(result.Units, detail)
	}
	sort.Slice(result.Units, func(i, j int) bool { return result.Units[i].UnitID < result.Units[j].UnitID })
	return result
}

func visibleAndLegalUnitIDs(c ai.DecisionContext) map[string]bool {
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
	result := UnitOverview{TrainingTicks: max(uint64(1), stats.TrainingTicks), ID: stats.ID, Cost: stats.Cost, Class: stats.Class}
	if card != nil {
		result.Role = card.Role
		result.Strengths = card.Strengths
		result.Weaknesses = card.Weaknesses
	}
	return result
}

func Compact(c ai.DecisionContext) Context {
	result := Context{
		FactionName: c.FactionName, Background: c.Background, Doctrine: c.Doctrine,
		ContinuousProduction: c.ContinuousProduction, Opening: c.Opening, OwnedUpgradeIDs: c.OwnedUpgradeIDs, SchemaVersion: ai.ContextSchema,
		RequestID: c.RequestID, BattleID: c.BattleID, FactionID: c.FactionID,
		TargetTick: c.TargetTick, ExpectedVersion: c.ExpectedVersion,
		ObservationVersion: c.ObservationVersion, ObservationHash: c.ObservationHash,
		View: c.View, Economy: c.Economy, Memory: c.Memory, ActivePlan: c.ActivePlan,
		Legal: LegalChoices{
			Buildings: c.Legal.Buildings, RecruitmentSources: c.Legal.RecruitmentSources,
			Upgrades: c.Legal.Upgrades, ActionKinds: c.Legal.ActionKinds,
			EconomySites: c.Legal.EconomySites, Units: []UnitOverview{},
		},
	}

	details := map[string]ai.UnitDetail{}
	if c.UnitReference != nil {
		result.Knowledge = &Knowledge{ReferenceIdentity: c.UnitReference.ReferenceIdentity}
		for _, detail := range c.UnitReference.Units {
			details[detail.UnitID] = detail
		}
	}
	if c.Knowledge != nil {
		if result.Knowledge == nil {
			result.Knowledge = &Knowledge{ReferenceIdentity: ai.ReferenceIdentity{RulesetID: c.Knowledge.RulesetID, RulesetVersion: c.Knowledge.RulesetVersion, Digest: c.Knowledge.Digest}}
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

// Project every saved observation into the current format without changing storage.
func Observation(c ai.DecisionContext) any {
	return groupContextFactions(deduplicateContext(battlefieldContext{Context: Compact(c), View: compactBattlefieldView(c.View)}))
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
	ai.ReferenceIdentity
	Units []UnitDetailResult `json:"units"`
}

// Guidance offsets are UTF-8 byte offsets into the frozen card. A continuation
// repeats base stats and never truncates guidance without an explicit next offset.
func LookupUnitDetailsPage(c ai.DecisionContext, ids []string, offset int) (UnitDetailsResponse, error) {
	response := UnitDetailsResponse{Units: []UnitDetailResult{}}
	if err := validateUnitDetailsRequest(ids, offset); err != nil {
		return response, err
	}
	identity, details := unitDetailsReference(c)
	response.ReferenceIdentity = identity
	allowed := visibleAndLegalUnitIDs(c)
	for _, id := range ids {
		result := UnitDetailResult{UnitID: id}
		if detail, ok := details[id]; ok && allowed[id] {
			var err error
			result, err = unitDetailPage(detail, id, offset)
			if err != nil {
				return response, err
			}
		}
		response.Units = append(response.Units, result)
	}
	return boundUnitDetailsPage(response, offset)
}

func unitDetailsReference(c ai.DecisionContext) (ai.ReferenceIdentity, map[string]ai.UnitDetail) {
	var identity ai.ReferenceIdentity
	details := map[string]ai.UnitDetail{}
	if c.UnitReference != nil {
		identity = c.UnitReference.ReferenceIdentity
		for _, detail := range c.UnitReference.Units {
			details[detail.UnitID] = detail
		}
	} else {
		// Legacy records may only have some cards, but legal base stats remain authoritative.
		if c.Knowledge != nil {
			identity = ai.ReferenceIdentity{RulesetID: c.Knowledge.RulesetID, RulesetVersion: c.Knowledge.RulesetVersion, Digest: c.Knowledge.Digest}
			for _, card := range c.Knowledge.Cards {
				copy := card.UnitCard
				details[card.UnitID] = ai.UnitDetail{UnitID: card.UnitID, Stats: card.Stats, Card: &copy}
			}
		}
		for _, unit := range c.Legal.Units {
			detail := details[unit.ID]
			detail.UnitID = unit.ID
			detail.Stats = unit
			details[unit.ID] = detail
		}
	}
	return identity, details
}

func unitDetailPage(detail ai.UnitDetail, id string, offset int) (UnitDetailResult, error) {
	stats := detail.Stats
	result := UnitDetailResult{UnitID: id, Available: true, Stats: &stats}
	if detail.Card == nil {
		return result, nil
	}
	guidance := detail.Card.Guidance
	if offset > len(guidance) || (offset < len(guidance) && !utf8.RuneStart(guidance[offset])) {
		return UnitDetailResult{UnitID: id}, fmt.Errorf("invalid guidanceOffset")
	}
	result.GuidanceOffset = offset
	result.Guidance = guidance[offset:]
	return result, nil
}

func boundUnitDetailsPage(response UnitDetailsResponse, offset int) (UnitDetailsResponse, error) {
	payload, _ := json.Marshal(response)
	if len(payload) > UnitDetailsMaxBytes {
		if len(response.Units) > 1 {
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

func validateUnitDetailsRequest(ids []string, offset int) error {
	if offset < 0 || (offset > 0 && len(ids) != 1) {
		return fmt.Errorf("guidanceOffset requires one unit ID and a nonnegative offset")
	}
	if len(ids) < 1 || len(ids) > 4 {
		return fmt.Errorf("supply 1–4 distinct unit IDs")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || seen[id] {
			return fmt.Errorf("supply 1–4 distinct nonempty unit IDs")
		}
		seen[id] = true
	}
	return nil
}
