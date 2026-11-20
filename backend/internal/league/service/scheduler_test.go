// Checks scheduling passes: battles only into free engine and CLI slots, recovery of an
// interrupted start, retries and void fixtures, and progression to a champion.

package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/authn"
	battleservice "github.com/twhAuto/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twhAuto/autonomous_battlefield/backend/internal/league"
)

func runningHarness(t *testing.T, entrants int) *fixtureHarness {
	t.Helper()
	h := newHarness()
	if _, err := h.service.CreateLeague(actorContext(authn.Admin), validInput(entrants)); err != nil {
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

// battleID is the battle a fixture starts on the given attempt.
func (h *fixtureHarness) battleID(i, attempt int) string {
	return league.FixtureBattleID(h.stored().BattleSalt, h.stored().Fixtures[i].ID, attempt)
}

// ownBattle registers a battle the scheduler created for fixture i on the attempt.
func (h *fixtureHarness) ownBattle(i, attempt int, status string) string {
	l, id := h.stored(), h.battleID(i, attempt)
	f := l.Fixtures[i]
	h.repo.battles[id] = status
	h.repo.origins[id] = BattleProvenance{OwnerID: l.OwnerID, DecisionBudgetExempt: true, ScenarioID: f.ScenarioID,
		FactionIDs: []string{l.Entrants[f.Home].FactionID, l.Entrants[f.Away].FactionID}}
	return id
}

// finish marks a fixture's battle completed, the home side winning.
func (h *fixtureHarness) finish(i int) {
	l := h.stored()
	f := &l.Fixtures[i]
	id := h.battleID(i, f.Attempts+1)
	home, away := l.Entrants[f.Home].FactionID, l.Entrants[f.Away].FactionID
	f.Status, f.Attempts, f.BattleID = league.FixtureCompleted, f.Attempts+1, &id
	f.Battle = &league.BattleOutcome{Status: league.BattleCompleted, WinnerFactionID: &home, Reason: "castle_destroyed",
		Factions: map[string]league.FactionScore{home: {CastleHealth: 100}, away: {}}}
	h.repo.battles[id] = league.BattleCompleted
}

func TestSchedulePassStartsTheFirstFixtureOnAnIdleEngine(t *testing.T) {
	h := runningHarness(t, 8)
	h.pass(t)
	first := h.stored().Fixtures[0]
	want := h.battleID(0, 1)
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
	h := runningHarness(t, 8)
	h.repo.battles["someone-elses"] = "running"
	h.pass(t)
	h.repo.battles["someone-elses"], h.repo.lockHeld = league.BattleCompleted, true
	h.pass(t)
	if len(h.battles.created) != 0 || h.stored().Fixtures[0].Status != league.FixturePending {
		t.Fatalf("started while busy or unlocked: %+v", h.stored().Fixtures[0])
	}
}

func TestSchedulePassLinksTheBattleOfAnInterruptedStart(t *testing.T) {
	h := runningHarness(t, 8)
	id := h.ownBattle(0, 1, "running")
	h.pass(t)
	if f := h.stored().Fixtures[0]; len(h.battles.created) != 0 || f.Status != league.FixtureRunning || *f.BattleID != id || f.Attempts != 1 {
		t.Fatalf("created %d, fixture %+v", len(h.battles.created), f)
	}
}

func TestSchedulePassNeverAdoptsAForeignBattle(t *testing.T) {
	foreign := map[string]func(*BattleProvenance){
		"other owner":      func(b *BattleProvenance) { b.OwnerID = "intruder" },
		"not exempt":       func(b *BattleProvenance) { b.DecisionBudgetExempt = false },
		"other map":        func(b *BattleProvenance) { b.ScenarioID = "elsewhere" },
		"sides swapped":    func(b *BattleProvenance) { b.FactionIDs[0], b.FactionIDs[1] = b.FactionIDs[1], b.FactionIDs[0] },
		"other faction":    func(b *BattleProvenance) { b.FactionIDs[1] = factionID(99) },
		"missing factions": func(b *BattleProvenance) { b.FactionIDs = nil },
	}
	for name, mutate := range foreign {
		h := runningHarness(t, 8)
		id := h.ownBattle(0, 1, league.BattleCompleted)
		origin := h.repo.origins[id]
		mutate(&origin)
		h.repo.origins[id] = origin
		h.pass(t)
		if f := h.stored().Fixtures[0]; f.Status != league.FixturePending || f.Attempts != 1 || f.BattleID != nil || len(h.battles.created) != 0 {
			t.Fatalf("%s: adopted %+v", name, f)
		}
		h.pass(t)
		if f := h.stored().Fixtures[0]; len(h.battles.created) != 1 || *f.BattleID != h.battleID(0, 2) {
			t.Fatalf("%s: retry = %+v", name, f)
		}
	}
}

func TestSchedulePassStopsBattlesLeftInCancelledLeagues(t *testing.T) {
	h := runningHarness(t, 8)
	l := h.stored()
	id := h.ownBattle(0, 1, "running")
	l.Fixtures[0].Status, l.Fixtures[0].BattleID, l.Status = league.FixtureCancelled, &id, league.StatusCancelled
	h.battles.stopErrs = map[string]error{id: errDatabase}
	h.pass(t)
	h.battles.stopErrs = nil
	h.pass(t)
	if fmt.Sprint(h.battles.stopped) != fmt.Sprint([]string{id, id}) || h.repo.battles[id] != "stopped" || h.battles.stoppers[0].Role != authn.Admin {
		t.Fatalf("stopped %v as %+v, status %s", h.battles.stopped, h.battles.stoppers, h.repo.battles[id])
	}
	h.pass(t)
	if len(h.battles.stopped) != 2 || len(h.battles.created) != 0 {
		t.Fatalf("stopped %v, created %d after the stop", h.battles.stopped, len(h.battles.created))
	}
}

func TestFinishedBattlesCompleteRetryOrVoidTheirFixtures(t *testing.T) {
	// Sixteen entrants open with eight fixtures, so five can be in play at once.
	h := runningHarness(t, 16)
	l := h.stored()
	running := func(i, attempts int, status string) {
		id := h.battleID(i, attempts)
		l.Fixtures[i].Status, l.Fixtures[i].Attempts, l.Fixtures[i].BattleID = league.FixtureRunning, attempts, &id
		if status != "" {
			l.Fixtures[i].Battle = &league.BattleOutcome{Status: status}
			h.ownBattle(i, attempts, status)
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
	if f := h.stored().Fixtures[1]; len(h.battles.created) != 1 || f.Attempts != 2 || *f.BattleID != h.battleID(1, 2) {
		t.Fatalf("retry = %+v", f)
	}
}

func TestStartFailuresSpendAttemptsOnlyWhenPermanent(t *testing.T) {
	h := runningHarness(t, 8)
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

func TestSchedulePassOpensEachRoundAndCrownsTheChampion(t *testing.T) {
	h := runningHarness(t, 8)
	// Each pass settles the fixtures finished before it and opens what follows: both
	// groups' winners' and losers' matches, their deciders, the semifinals and the final.
	for _, want := range []int{8, 10, 12, 13} {
		for i := range h.stored().Fixtures {
			if !h.stored().Fixtures[i].Settled() {
				h.finish(i)
			}
		}
		h.pass(t)
		if l := h.stored(); len(l.Fixtures) != want || l.Status != league.StatusRunning {
			t.Fatalf("%s league has %d fixtures; want %d", l.Status, len(l.Fixtures), want)
		}
	}
	if l := h.stored(); l.Fixtures[10].Stage != league.StageSemifinal || l.Fixtures[12].Stage != league.StageFinal {
		t.Fatalf("knockout = %+v", l.Fixtures[10:])
	}
	h.finish(12)
	h.pass(t)
	l := h.stored()
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
	h := runningHarness(t, 8)
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

// slotHarness runs a 16-entrant league with the given engine slots; Claude entrants are
// CLI models.
func slotHarness(t *testing.T, slots int) *fixtureHarness {
	t.Helper()
	h := newHarnessWith(Options{BattleSlots: slots, Profiles: fakeProfiles{"claude-subscription-local": "cli"}})
	if _, err := h.service.CreateLeague(actorContext(authn.Admin), validInput(16)); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *fixtureHarness) running() []int {
	indexes := []int{}
	for i, f := range h.stored().Fixtures {
		if f.Status == league.FixtureRunning {
			indexes = append(indexes, i)
		}
	}
	return indexes
}

// cliFixture reports whether both sides of fixture i are Claude CLI entrants; in
// validInput(16) the even entrants, which make up groups A and C, are.
func (h *fixtureHarness) cliFixture(i int) bool {
	return h.stored().Fixtures[i].Home%2 == 0
}

func TestSchedulePassFillsFreeBattleSlots(t *testing.T) {
	h := newHarnessWith(Options{BattleSlots: 4})
	if _, err := h.service.CreateLeague(actorContext(authn.Admin), validInput(8)); err != nil {
		t.Fatal(err)
	}
	h.repo.battles["someone-elses"] = "running"
	h.pass(t)
	if got := h.running(); fmt.Sprint(got) != "[0 1 2]" || len(h.battles.created) != 3 {
		t.Fatalf("running %v, created %d; want the first three fixtures beside another owner's battle", got, len(h.battles.created))
	}
	h.pass(t)
	if len(h.battles.created) != 3 {
		t.Fatal("started a battle with every slot full")
	}
	l := h.stored()
	l.Fixtures[1].Battle = &league.BattleOutcome{Status: league.BattleCompleted, Factions: map[string]league.FactionScore{}}
	h.repo.battles[*l.Fixtures[1].BattleID] = league.BattleCompleted
	h.pass(t)
	if got := h.running(); fmt.Sprint(got) != "[0 2 3]" || h.stored().Fixtures[1].Status != league.FixtureCompleted {
		t.Fatalf("after one battle finished, running %v", got)
	}
}

func TestSchedulePassKeepsCLISidesWithinTheirSlots(t *testing.T) {
	h := slotHarness(t, 4)
	h.repo.cliSlots = 2
	h.pass(t)
	cli := 0
	for _, i := range h.running() {
		if h.cliFixture(i) {
			cli++
		}
	}
	if len(h.running()) != 4 || cli != 1 {
		t.Fatalf("running %v with %d CLI fixtures; want four battles, one with two CLI sides", h.running(), cli)
	}
	// One CLI slot cannot hold a two-sided CLI fixture, but the league must not stall.
	h = slotHarness(t, 1)
	h.repo.cliSlots = 1
	h.pass(t)
	if got := h.running(); len(got) != 1 || !h.cliFixture(got[0]) {
		t.Fatalf("running %v; want the first fixture started despite too few CLI slots", got)
	}
}

func TestSchedulePassStopsALeagueAfterAPermanentStartFailure(t *testing.T) {
	h := slotHarness(t, 4)
	h.battles.createErr = battleservice.ErrModelProfileUnavailable
	h.pass(t)
	spent := 0
	for _, f := range h.stored().Fixtures {
		spent += f.Attempts
	}
	if spent != 1 || h.stored().Fixtures[0].Attempts != 1 {
		t.Fatalf("attempts spent = %d; want only the first fixture's", spent)
	}
}

func TestSchedulePassAndSchedulingSurfaceCapacityErrors(t *testing.T) {
	h := slotHarness(t, 4)
	h.repo.capErr = errDatabase
	if err := h.service.SchedulePass(context.Background()); !errors.Is(err, errDatabase) || len(h.battles.created) != 0 {
		t.Fatalf("pass err = %v, created %d", err, len(h.battles.created))
	}
	if _, err := h.service.Scheduling(actorContext(authn.Tester)); !errors.Is(err, errDatabase) {
		t.Fatalf("scheduling err = %v", err)
	}
	h.repo.capErr = nil
	if got, err := h.service.Scheduling(actorContext(authn.Tester)); err != nil || got != (league.Scheduling{BattleSlots: 4, CLISlots: 4}) {
		t.Fatalf("scheduling = %+v, %v", got, err)
	}
	if _, err := h.service.Scheduling(actorContext(authn.CI)); !errors.Is(err, authn.ErrForbidden) {
		t.Fatalf("service identity scheduling err = %v", err)
	}
	if got := newHarness().service.battleSlots; got != 1 {
		t.Fatalf("default battle slots = %d, want 1", got)
	}
}
