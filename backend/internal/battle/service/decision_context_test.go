package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/ai"
	"github.com/twh09/autonomous_battlefield/backend/internal/ai/agentview"
	"github.com/twh09/autonomous_battlefield/backend/internal/faction"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
)

const contextBattleID = "7f22e693-bacd-46bb-b942-64fa613cd388"
const contextRequestID = "4db8d152-1e04-4d10-8824-859c6ed72878"

type contextRepo struct {
	*fakeBattleRepo
	record ai.DecisionRecord
	err    error
}

func (r *contextRepo) GetDecision(context.Context, string) (ai.DecisionRecord, error) {
	return r.record, r.err
}

func savedRecord() ai.DecisionRecord {
	return ai.DecisionRecord{ID: contextRequestID, BattleID: contextBattleID, Attempt: 2, FactionID: "a", TargetTick: 10,
		Observation:      ai.DecisionContext{BattleID: contextBattleID, FactionID: "a", TargetTick: 10, ObservationVersion: 8, FactionName: "Saved name", Background: "Saved background", Doctrine: "Saved doctrine", Economy: ai.EconomyContext{Gold: 35}, View: game.View{State: game.State{BattleID: contextBattleID, Tick: 8}}},
		ProviderMetadata: map[string]any{"secret": "private-marker"}, CapabilityHash: "private-marker", Traceparent: "private-marker", IdempotencyKey: "private-marker"}
}

func TestSavedDecisionContextProjection(t *testing.T) {
	repo := &contextRepo{fakeBattleRepo: &fakeBattleRepo{}, record: savedRecord()}
	service := NewService(repo, &battleFactionRepo{}, &fakeScenarioCatalog{}, 100).(DecisionContextService)
	result, err := service.GetDecisionContext(context.Background(), contextBattleID, contextRequestID, 2)
	if err != nil || result.Context == nil || result.Context.Economy.Gold != 35 || result.ObservationTick != 8 || result.TargetTick != 10 || result.Context.Background != "Saved background" {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	// Reconstructed context includes public submission metadata, never capabilities.
	for _, field := range []string{"private-marker", "providerMetadata", "capability", "traceparent", "idempotencyKey"} {
		if strings.Contains(string(data), field) {
			t.Fatalf("private field %s exposed: %s", field, data)
		}
	}
	repo.record.Observation = ai.DecisionContext{}
	result, err = service.GetDecisionContext(context.Background(), contextBattleID, contextRequestID, 2)
	if err != nil || result.Context != nil {
		t.Fatalf("legacy observation: %+v %v", result, err)
	}
}

func TestSavedDecisionContextValidationAndScope(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*ai.DecisionRecord)
	}{
		{"battle", func(r *ai.DecisionRecord) { r.BattleID = "another" }},
		{"attempt", func(r *ai.DecisionRecord) { r.Attempt = 1 }},
		{"request", func(r *ai.DecisionRecord) { r.ID = "another" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &contextRepo{fakeBattleRepo: &fakeBattleRepo{}, record: savedRecord()}
			tc.mutate(&repo.record)
			service := NewService(repo, &battleFactionRepo{}, &fakeScenarioCatalog{}, 100).(DecisionContextService)
			if _, err := service.GetDecisionContext(context.Background(), contextBattleID, contextRequestID, 2); !errors.Is(err, ai.ErrDecisionNotFound) {
				t.Fatal(err)
			}
		})
	}
	repo := &contextRepo{fakeBattleRepo: &fakeBattleRepo{}, record: savedRecord()}
	service := NewService(repo, &battleFactionRepo{}, &fakeScenarioCatalog{}, 100).(DecisionContextService)
	for _, args := range []struct {
		battle, request string
		attempt         int
	}{{"bad", contextRequestID, 2}, {contextBattleID, "bad", 2}, {contextBattleID, contextRequestID, 0}} {
		if _, err := service.GetDecisionContext(context.Background(), args.battle, args.request, args.attempt); !errors.Is(err, ErrInvalidBattle) {
			t.Fatal(err)
		}
	}
	repo.record.Observation.FactionID = "another"
	if _, err := service.GetDecisionContext(context.Background(), contextBattleID, contextRequestID, 2); err == nil {
		t.Fatal("accepted inconsistent observation")
	}
	repo.err = errors.New("database unavailable")
	if _, err := service.GetDecisionContext(context.Background(), contextBattleID, contextRequestID, 2); !errors.Is(err, repo.err) {
		t.Fatal(err)
	}
	service = NewService(&fakeBattleRepo{}, &battleFactionRepo{}, &fakeScenarioCatalog{}, 100).(DecisionContextService)
	if _, err := service.GetDecisionContext(context.Background(), contextBattleID, contextRequestID, 2); !errors.Is(err, ai.ErrDecisionNotFound) {
		t.Fatal(err)
	}
}

func TestBattleProfileSnapshotSurvivesFactionChanges(t *testing.T) {
	snapshot := game.FactionSetup{ID: "a", Name: "Original", Background: "Original history", Doctrine: "Original doctrine"}
	repository := &fakeBattleRepo{run: &BattleRun{ID: contextBattleID, ScenarioSnapshot: game.Scenario{Factions: []game.FactionSetup{snapshot}}}}
	factions := &battleFactionRepo{factions: map[string]*faction.Faction{"a": {ID: "a", Name: "Renamed", Background: "Edited history", Doctrine: "Edited doctrine"}}}
	service := NewService(repository, factions, &fakeScenarioCatalog{}, 100)
	for _, deleted := range []bool{false, true} {
		if deleted {
			delete(factions.factions, "a")
		}
		run, err := service.GetBattle(context.Background(), contextBattleID)
		if err != nil {
			t.Fatal(err)
		}
		saved := run.ScenarioSnapshot.Factions[0]
		if saved.Name != snapshot.Name || saved.Background != snapshot.Background || saved.Doctrine != snapshot.Doctrine {
			t.Fatalf("deleted=%v snapshot=%+v", deleted, saved)
		}
	}
}

func TestCompactSavedDecisionContext(t *testing.T) {
	record := savedRecord()
	record.Observation.SchemaVersion = ai.ContextSchema
	rules := game.Ruleset{ID: "r", Version: "1", Units: []game.UnitType{{ID: "ranger", Cost: 10, Attack: 20}}}
	record.Observation.Legal.Units = rules.Units
	record.Observation.UnitReference = agentview.FreezeUnitReference(rules, record.Observation)
	repo := &contextRepo{fakeBattleRepo: &fakeBattleRepo{}, record: record}
	svc := NewService(repo, &battleFactionRepo{}, &fakeScenarioCatalog{}, 100).(DecisionContextService)
	result, err := svc.GetDecisionContext(context.Background(), contextBattleID, contextRequestID, 2)
	if err != nil || result.InitialContext == nil || result.AvailableUnitDetails == nil {
		t.Fatal(result, err)
	}
	initial, _ := json.Marshal(result.InitialContext)
	expected, _ := json.Marshal(agentview.Observation(record.Observation))
	if string(initial) != string(expected) || strings.Contains(string(initial), `"attack":20`) {
		t.Fatal("incorrect initial projection")
	}
	if result.AvailableUnitDetails.Units[0].Stats.Attack != 20 {
		t.Fatal("lost optional reference")
	}
}
