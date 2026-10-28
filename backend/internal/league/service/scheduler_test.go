// Checks scheduling passes: one battle at a time only on an idle engine, recovery of an
// interrupted start, retries and void fixtures, and progression to a champion.

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/league"
)

func runningHarness(t *testing.T) *fixtureHarness {
	t.Helper()
	h := newHarness()
	if _, err := h.service.CreateLeague(actorContext(authn.Admin), validInput(8)); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *fixtureHarness) stored() *league.League { return h.repo.leagues[leagueID] }

func (h *fixtureHarness) pass(t *testing.T) {
	t.Helper()
	if err := h.service.SchedulePass(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// finish marks a fixture's battle completed, the home side winning.
func (h *fixtureHarness) finish(i int) {
	l := h.stored()
	f := &l.Fixtures[i]
	id := league.FixtureBattleID(f.ID, f.Attempts+1)
	home, away := l.Entrants[f.Home].FactionID, l.Entrants[f.Away].FactionID
	f.Status, f.Attempts, f.BattleID = league.FixtureCompleted, f.Attempts+1, &id
	f.Battle = &league.BattleOutcome{Status: league.BattleCompleted, WinnerFactionID: &home, Reason: "castle_destroyed",
		Factions: map[string]league.FactionScore{home: {CastleHealth: 100}, away: {}}}
	h.repo.battles[id] = league.BattleCompleted
}

func TestSchedulePassStartsTheFirstFixtureOnAnIdleEngine(t *testing.T) {
	h := runningHarness(t)
	h.pass(t)
	first := h.stored().Fixtures[0]
	want := league.FixtureBattleID(first.ID, 1)
	if len(h.battles.created) != 1 || first.Status != league.FixtureRunning || first.Attempts != 1 || *first.BattleID != want {
		t.Fatalf("created %d, fixture %+v", len(h.battles.created), first)
	}
	input, creator := h.battles.created[0], h.battles.creators[0]
	l := h.stored()
	if input.ID != want || !input.DecisionBudgetExempt || input.ScenarioID != first.ScenarioID || input.TickCount != 500 ||
		input.Participants[0].FactionID != l.Entrants[first.Home].FactionID || input.Participants[1].Controller != l.Entrants[first.Away].Controller {
		t.Fatalf("battle input = %+v", input)
	}
	if creator.ID != "actor-admin" || creator.Role != authn.Admin {
		t.Fatalf("battle created as %+v", creator)
	}
	h.pass(t)
	if len(h.battles.created) != 1 {
		t.Fatal("started a second battle while the first was queued")
	}
}

func TestSchedulePassWaitsForTheEngineAndTheLock(t *testing.T) {
	h := runningHarness(t)
	h.repo.busy = true
	h.pass(t)
	h.repo.busy, h.repo.lockHeld = false, true
	h.pass(t)
	if len(h.battles.created) != 0 || h.stored().Fixtures[0].Status != league.FixturePending {
		t.Fatalf("started while busy or unlocked: %+v", h.stored().Fixtures[0])
	}
}

func TestSchedulePassLinksTheBattleOfAnInterruptedStart(t *testing.T) {
	h := runningHarness(t)
	id := league.FixtureBattleID(h.stored().Fixtures[0].ID, 1)
	h.repo.battles[id] = "running"
	h.pass(t)
	if f := h.stored().Fixtures[0]; len(h.battles.created) != 0 || f.Status != league.FixtureRunning || *f.BattleID != id || f.Attempts != 1 {
		t.Fatalf("created %d, fixture %+v", len(h.battles.created), f)
	}
}

func TestFinishedBattlesCompleteRetryOrVoidTheirFixtures(t *testing.T) {
	h := runningHarness(t)
	l := h.stored()
	running := func(i, attempts int, status string) {
		id := league.FixtureBattleID(l.Fixtures[i].ID, attempts)
		l.Fixtures[i].Status, l.Fixtures[i].Attempts, l.Fixtures[i].BattleID = league.FixtureRunning, attempts, &id
		if status != "" {
			l.Fixtures[i].Battle = &league.BattleOutcome{Status: status}
			h.repo.battles[id] = status
		}
	}
	running(0, 1, league.BattleCompleted)
	running(1, 1, "failed")
	running(2, 2, "stopped")
	running(3, 1, "")
	running(4, 1, "queued")
	h.pass(t)
	got := h.stored().Fixtures
	if got[0].Status != league.FixtureCompleted || got[2].Status != league.FixtureVoid || got[4].Status != league.FixtureRunning {
		t.Fatalf("statuses %s %s %s", got[0].Status, got[2].Status, got[4].Status)
	}
	for _, i := range []int{1, 3} {
		if got[i].Status != league.FixturePending || got[i].BattleID != nil || got[i].Attempts != 1 {
			t.Fatalf("fixture %d = %+v", i, got[i])
		}
	}
	if len(h.battles.created) != 0 {
		t.Fatal("started a battle while one was queued")
	}
	delete(h.repo.battles, *got[4].BattleID)
	h.pass(t)
	if f := h.stored().Fixtures[1]; len(h.battles.created) != 1 || f.Attempts != 2 || *f.BattleID != league.FixtureBattleID(f.ID, 2) {
		t.Fatalf("retry = %+v", f)
	}
}

func TestStartFailuresSpendAttemptsOnlyWhenPermanent(t *testing.T) {
	h := runningHarness(t)
	h.battles.createErr = errDatabase
	if err := h.service.SchedulePass(context.Background()); !errors.Is(err, errDatabase) || h.stored().Fixtures[0].Attempts != 0 {
		t.Fatalf("transient err = %v, fixture %+v", err, h.stored().Fixtures[0])
	}
	h.battles.createErr = battleservice.ErrModelProfileUnavailable
	h.pass(t)
	if f := h.stored().Fixtures[0]; f.Status != league.FixturePending || f.Attempts != 1 {
		t.Fatalf("first failure = %+v", f)
	}
	h.pass(t)
	if f := h.stored().Fixtures[0]; f.Status != league.FixtureVoid || f.Attempts != 2 {
		t.Fatalf("second failure = %+v", f)
	}
	h.battles.createErr = nil
	h.pass(t)
	if h.stored().Fixtures[1].Status != league.FixtureRunning {
		t.Fatalf("next fixture = %+v", h.stored().Fixtures[1])
	}
}

func TestSchedulePassOpensTheKnockoutAndCrownsTheChampion(t *testing.T) {
	h := runningHarness(t)
	for i := range h.stored().Fixtures {
		h.finish(i)
	}
	h.pass(t)
	l := h.stored()
	if len(l.Fixtures) != 28 || l.Fixtures[24].Stage != league.StageSemifinal || l.Fixtures[24].Status != league.FixtureRunning {
		t.Fatalf("after groups: %d fixtures, first semifinal %+v", len(l.Fixtures), l.Fixtures[24])
	}
	for stage := 0; stage < 2; stage++ {
		for i := range h.stored().Fixtures {
			if !h.stored().Fixtures[i].Settled() {
				h.finish(i)
			}
		}
		h.pass(t)
	}
	l = h.stored()
	if l.Status != league.StatusCompleted || l.Champion == nil || len(l.Fixtures) != league.TotalFixtures(2) {
		t.Fatalf("league %s champion %v fixtures %d", l.Status, l.Champion, len(l.Fixtures))
	}
	created := len(h.battles.created)
	h.pass(t)
	if len(h.battles.created) != created {
		t.Fatal("a completed league started another battle")
	}
}

func TestRunSchedulerPassesUntilCancelled(t *testing.T) {
	h := runningHarness(t)
	h.repo.storeErr = errDatabase
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); h.service.RunScheduler(ctx, time.Millisecond) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scheduler did not stop")
	}
}
