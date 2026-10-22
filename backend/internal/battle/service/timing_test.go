package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	"github.com/twh09/autonomous_battlefield/backend/internal/faction"
	"github.com/twh09/autonomous_battlefield/backend/internal/game"
)

func TestBattleTimingValidation(t *testing.T) {
	for _, duration := range []string{"", "invalid", "0s", "-1s", "99ms", "11s", "99999999999999999999h"} {
		t.Run(duration, func(t *testing.T) {
			scenario := game.Scenario{}
			if err := applyBattleTiming(&scenario, CreateBattleInput{TickDuration: &duration}); !errors.Is(err, ErrInvalidBattle) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestBattleTimingRejectsInvalidIntervals(t *testing.T) {
	for _, interval := range []uint64{0, 101, ^uint64(0)} {
		scenario := game.Scenario{}
		if err := applyBattleTiming(&scenario, CreateBattleInput{DecisionIntervalTicks: &interval}); !errors.Is(err, ErrInvalidBattle) {
			t.Fatalf("interval %d: error = %v", interval, err)
		}
	}
}

func TestBattleTimingAcceptsBounds(t *testing.T) {
	for _, duration := range []string{"100ms", "10s"} {
		for _, interval := range []uint64{1, 100} {
			scenario := game.Scenario{}
			if err := applyBattleTiming(&scenario, CreateBattleInput{TickDuration: &duration, DecisionIntervalTicks: &interval}); err != nil {
				t.Fatal(err)
			}
			want, _ := time.ParseDuration(duration)
			if scenario.TickDuration != want || uint64(scenario.DecisionIntervalTicks) != interval {
				t.Fatalf("timing = %v/%v", scenario.TickDuration, scenario.DecisionIntervalTicks)
			}
		}
	}
}

func TestCreateBattleFreezesTimingAndRejectsChangedRetry(t *testing.T) {
	repo := &fakeBattleRepo{}
	factions := &battleFactionRepo{factions: map[string]*faction.Faction{
		serviceParticipantIDs[0]: {ID: serviceParticipantIDs[0], Name: "North"},
		serviceParticipantIDs[1]: {ID: serviceParticipantIDs[1], Name: "South"},
	}}
	catalog := &fakeScenarioCatalog{resolved: game.Scenario{ID: "castle-siege-v1"}}
	svc := NewService(repo, factions, catalog, 1000, Options{})
	ctx := authn.WithActor(context.Background(), authn.Actor{ID: "admin", Role: authn.Admin})
	input := CreateBattleInput{ID: "7f22e693-bacd-46bb-b942-64fa613cd388", ScenarioID: "castle-siege-v1", Participants: scriptedParticipants(), TickCount: 100}
	defaults, err := svc.CreateBattle(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	duration, interval := "500ms", uint64(5)
	input.TickDuration, input.DecisionIntervalTicks = &duration, &interval
	run, err := svc.CreateBattle(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := repo.created.ScenarioSnapshot
	if snapshot.TickDuration != 500*time.Millisecond || snapshot.DecisionIntervalTicks != 5 || run.ScenarioDigest == defaults.ScenarioDigest {
		t.Fatalf("timing/digest not frozen: %#v", run)
	}
	if catalog.resolved.TickDuration != defaults.ScenarioSnapshot.TickDuration || catalog.resolved.DecisionIntervalTicks != 10 {
		t.Fatal("catalog defaults changed")
	}
	match, err := game.NewMatch(run.ID, snapshot, input.TickCount)
	if err != nil || match.TickDuration() != 500*time.Millisecond || match.Scenario().DecisionIntervalTicks != 5 {
		t.Fatalf("restored match does not use timing: %v", err)
	}
	repo.run, repo.createErr = run, ErrBattleAlreadyExists
	duration = "0.5s"
	if _, err := svc.CreateBattle(ctx, input); err != nil {
		t.Fatalf("equivalent retry: %v", err)
	}
	interval = 10
	if _, err := svc.CreateBattle(ctx, input); !errors.Is(err, ErrBattleAlreadyExists) {
		t.Fatalf("changed retry: %v", err)
	}
	duration = "0s"
	if _, err := svc.CreateBattle(ctx, input); !errors.Is(err, ErrInvalidBattle) {
		t.Fatalf("invalid override: %v", err)
	}
}
