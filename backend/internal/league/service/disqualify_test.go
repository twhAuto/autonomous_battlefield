// Checks disqualifying league entrants: permissions and refusals, forfeited battles that
// stop at once or on a later pass, and walkovers that carry a league to its champion.

package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	"github.com/twh09/autonomous_battlefield/backend/internal/league"
)

func TestDisqualifyEntrantForfeitsAndStopsItsBattle(t *testing.T) {
	h := runningHarness(t, 8)
	h.pass(t)
	first := h.stored().Fixtures[0]
	battle := *first.BattleID
	got, err := h.service.DisqualifyEntrant(actorContext(authn.Admin), strings.ToUpper(leagueID), first.Away, "  too slow ")
	if err != nil {
		t.Fatal(err)
	}
	d, forfeited := got.Entrants[first.Away].Disqualified, got.Fixtures[0]
	if d == nil || d.By != "actor-admin" || d.Reason != "too slow" || d.At.IsZero() || forfeited.Status != league.FixtureForfeited ||
		forfeited.Forfeit != league.ForfeitAway || *forfeited.BattleID != battle || got.MatchWinner(forfeited) != first.Home {
		t.Fatalf("disqualification %+v, fixture %+v", d, forfeited)
	}
	if fmt.Sprint(h.battles.stopped) != fmt.Sprint([]string{battle}) || h.repo.battles[battle] != "stopped" || h.battles.stoppers[0].ID != "actor-admin" {
		t.Fatalf("stopped %v as %+v", h.battles.stopped, h.battles.stoppers)
	}
	again, err := h.service.DisqualifyEntrant(actorContext(authn.Admin), leagueID, first.Away, "another reason")
	if err != nil || again.Entrants[first.Away].Disqualified.Reason != "too slow" || len(h.battles.stopped) != 1 {
		t.Fatalf("repeat err = %v, entrant %+v, stopped %v", err, again.Entrants[first.Away], h.battles.stopped)
	}
	// The freed engine slot takes the next opening.
	h.pass(t)
	if len(h.battles.created) != 2 || h.stored().Fixtures[1].Status != league.FixtureRunning {
		t.Fatalf("created %d, fixtures %+v", len(h.battles.created), h.stored().Fixtures[:2])
	}
}

func TestDisqualifyEntrantRefusals(t *testing.T) {
	h := runningHarness(t, 8)
	admin := actorContext(authn.Admin)
	cases := map[string]struct {
		ctx     context.Context
		id      string
		entrant int
		reason  string
		want    error
	}{
		"developer":       {actorContext(authn.Developer), leagueID, 0, "", authn.ErrForbidden},
		"malformed ID":    {admin, "league", 0, "", ErrInvalidLeague},
		"long reason":     {admin, leagueID, 0, strings.Repeat("é", maxReasonLength+1), ErrInvalidLeague},
		"unknown league":  {admin, "9d7a1d2c-6c43-4cf8-9a6e-0c5a1f3b2e11", 0, "", ErrLeagueNotFound},
		"unknown entrant": {admin, leagueID, 8, "", league.ErrEntrantNotFound},
	}
	for name, c := range cases {
		if _, err := h.service.DisqualifyEntrant(c.ctx, c.id, c.entrant, c.reason); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", name, err, c.want)
		}
	}
	h.repo.storeErr = errDatabase
	if _, err := h.service.DisqualifyEntrant(admin, leagueID, 0, ""); !errors.Is(err, errDatabase) {
		t.Fatalf("store err = %v", err)
	}
	h.repo.storeErr = nil
	if _, err := h.service.DisqualifyEntrant(admin, leagueID, 0, strings.Repeat("é", maxReasonLength)); err != nil {
		t.Fatalf("longest reason: %v", err)
	}
	h.stored().Status = league.StatusCancelled
	if _, err := h.service.DisqualifyEntrant(admin, leagueID, 1, ""); !errors.Is(err, ErrLeagueNotActive) {
		t.Fatalf("cancelled league err = %v", err)
	}
}

func TestDisqualifyEntrantStopsTheBattleOfAnInterruptedStart(t *testing.T) {
	h := runningHarness(t, 8)
	// A pass created fixture 0's battle, then rolled back before linking it.
	battle := h.ownBattle(0, 1, "queued")
	if _, err := h.service.DisqualifyEntrant(actorContext(authn.Admin), leagueID, h.stored().Fixtures[0].Away, ""); err != nil {
		t.Fatal(err)
	}
	f := h.stored().Fixtures[0]
	if f.Status != league.FixtureForfeited || f.BattleID == nil || *f.BattleID != battle || f.Attempts != 1 ||
		fmt.Sprint(h.battles.stopped) != fmt.Sprint([]string{battle}) || h.repo.battles[battle] != "stopped" {
		t.Fatalf("fixture %+v, stopped %v", f, h.battles.stopped)
	}
	// A foreign battle holding the next opening's ID is neither linked nor stopped.
	other := h.ownBattle(1, 1, "running")
	h.repo.origins[other] = BattleProvenance{OwnerID: "intruder"}
	if _, err := h.service.DisqualifyEntrant(actorContext(authn.Admin), leagueID, h.stored().Fixtures[1].Away, ""); err != nil {
		t.Fatal(err)
	}
	if f := h.stored().Fixtures[1]; f.Status != league.FixtureForfeited || f.BattleID != nil || len(h.battles.stopped) != 1 {
		t.Fatalf("fixture %+v, stopped %v", f, h.battles.stopped)
	}
}

func TestSchedulePassRetriesTheStopOfAForfeitedBattle(t *testing.T) {
	h := runningHarness(t, 8)
	h.pass(t)
	first := h.stored().Fixtures[0]
	battle := *first.BattleID
	h.battles.stopErrs = map[string]error{battle: errDatabase}
	if _, err := h.service.DisqualifyEntrant(actorContext(authn.Admin), leagueID, first.Home, ""); err != nil {
		t.Fatal(err)
	}
	h.battles.stopErrs = nil
	h.pass(t)
	if fmt.Sprint(h.battles.stopped) != fmt.Sprint([]string{battle, battle}) || h.repo.battles[battle] != "stopped" || h.stored().Fixtures[0].Status != league.FixtureForfeited {
		t.Fatalf("stopped %v, battle %s, fixture %+v", h.battles.stopped, h.repo.battles[battle], h.stored().Fixtures[0])
	}
}

func TestWalkoversCarryALeagueToAChampionWhoWasNotDisqualified(t *testing.T) {
	h := runningHarness(t, 8)
	// Group A is entrants 0, 2, 4, 6 and group B 1, 3, 5, 7.
	for _, entrant := range []int{0, 2, 1} {
		if _, err := h.service.DisqualifyEntrant(actorContext(authn.Admin), leagueID, entrant, ""); err != nil {
			t.Fatal(err)
		}
	}
	// Group A's openings were both forfeited, so its next round opened at once.
	if l := h.stored(); len(l.Fixtures) != 6 || l.Fixtures[5].Forfeit != league.ForfeitBoth {
		t.Fatalf("fixtures = %+v", l.Fixtures)
	}
	for range league.TotalFixtures(2) {
		h.finishAll()
		h.pass(t)
	}
	l := h.stored()
	if l.Status != league.StatusCompleted || l.Champion == nil || l.Disqualified(*l.Champion) || len(l.Fixtures) != league.TotalFixtures(2) {
		t.Fatalf("league %s, champion %v, %d fixtures", l.Status, l.Champion, len(l.Fixtures))
	}
	started := map[string]bool{}
	for _, input := range h.battles.created {
		started[input.ID] = true
	}
	for _, f := range l.Fixtures {
		if f.Status == league.FixtureForfeited && started[league.FixtureBattleID(l.BattleSalt, f.ID, 1)] {
			t.Fatalf("started a battle for walkover %+v", f)
		}
	}
}

// finishAll completes every unsettled fixture, the home side winning.
func (h *fixtureHarness) finishAll() {
	for i, f := range h.stored().Fixtures {
		if !f.Settled() {
			h.finish(i)
		}
	}
}
