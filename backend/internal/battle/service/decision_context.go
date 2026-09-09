// Projects persisted decision observations for the operator inspector without exposing
// provider metadata, trace state, or mutation capabilities.
package service

import (
	"context"
	"errors"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
	"github.com/twh09/autonomous_battlefield/backend/internal/game/controller"
)

type DecisionContextService interface {
	GetDecisionContext(context.Context, string, string, int) (SavedDecisionContext, error)
}

type decisionContextRepository interface {
	GetDecision(context.Context, string) (ai.DecisionRecord, error)
}

type SavedDecisionContext struct {
	InitialContext       any                       `json:"initialContext,omitempty"`
	AvailableUnitDetails *ai.UnitReferenceSnapshot `json:"availableUnitDetails,omitempty"`
	RequestID            string                    `json:"requestId"`
	BattleID             string                    `json:"battleId"`
	FactionID            string                    `json:"factionId"`
	Attempt              int                       `json:"attempt"`
	TargetTick           uint64                    `json:"targetTick"`
	ObservationTick      uint64                    `json:"observationTick"`
	Context              *DecisionObservation      `json:"context"`
}

type DecisionObservation struct {
	FactionName     string                  `json:"factionName,omitempty"`
	Background      string                  `json:"background,omitempty"`
	Doctrine        string                  `json:"doctrine,omitempty"`
	Opening         bool                    `json:"opening"`
	OwnedUpgradeIDs []string                `json:"ownedUpgradeIds"`
	Economy         ai.EconomyContext       `json:"economy"`
	View            game.View               `json:"view"`
	Legal           any                     `json:"legal"`
	ActivePlan      controller.PlanProposal `json:"activePlan"`
	Memory          []ai.FactionMemoryEntry `json:"memory,omitempty"`
	Knowledge       any                     `json:"knowledge,omitempty"`
}

func (s *battleService) GetDecisionContext(ctx context.Context, battleID, requestID string, attempt int) (SavedDecisionContext, error) {
	if !battleUUIDPattern.MatchString(battleID) || !battleUUIDPattern.MatchString(requestID) || attempt < 1 {
		return SavedDecisionContext{}, ErrInvalidBattle
	}
	repository, ok := s.battles.(decisionContextRepository)
	if !ok {
		return SavedDecisionContext{}, ai.ErrDecisionNotFound
	}
	record, err := repository.GetDecision(ctx, requestID)
	if err != nil {
		return SavedDecisionContext{}, err
	}
	if record.ID != requestID || record.BattleID != battleID || record.Attempt != attempt {
		return SavedDecisionContext{}, ai.ErrDecisionNotFound
	}
	observation := record.Observation
	result := SavedDecisionContext{RequestID: record.ID, BattleID: record.BattleID, FactionID: record.FactionID, Attempt: record.Attempt, TargetTick: record.TargetTick, ObservationTick: observation.ObservationVersion}
	// Older records may have no saved observation. Never synthesize one from live state.
	if observation.BattleID == "" {
		return result, nil
	}
	if observation.BattleID != battleID || observation.FactionID != record.FactionID || observation.TargetTick != record.TargetTick {
		return SavedDecisionContext{}, errors.New("saved observation scope mismatch")
	}
	result.Context = &DecisionObservation{
		FactionName: observation.FactionName, Background: observation.Background, Doctrine: observation.Doctrine,
		Opening: observation.Opening, OwnedUpgradeIDs: observation.OwnedUpgradeIDs,
		Economy: observation.Economy, View: observation.View, Legal: observation.Legal,
		ActivePlan: observation.ActivePlan, Memory: observation.Memory,
	}
	compact := ai.CompactContext(observation)
	result.InitialContext = ai.AgentObservation(observation)
	result.AvailableUnitDetails = observation.UnitReference
	result.Context.Legal = compact.Legal
	result.Context.Knowledge = compact.Knowledge
	return result, nil
}
