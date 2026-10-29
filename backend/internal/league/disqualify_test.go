// Checks disqualification: forfeits of open and later matches, results that stand, the
// refusals, matches between two disqualified entrants and a champion who was never
// disqualified.

package league

import (
	"errors"
	"testing"
)

func disqualify(t *testing.T, l *League, entrant int) []Fixture {
	t.Helper()
	forfeited, err := l.Disqualify(entrant, Disqualification{By: "admin", Reason: "too slow"})
	if err != nil {
		t.Fatalf("disqualify %d: %v", entrant, err)
	}
	return forfeited
}

// fixtureOf finds an opened fixture between two entrants.
func fixtureOf(t *testing.T, l *League, a, b int) Fixture {
	t.Helper()
	for _, f := range l.Fixtures {
		if pairOf(f) == [2]int{min(a, b), max(a, b)} {
			return f
		}
	}
	t.Fatalf("no fixture between %d and %d in %+v", a, b, l.Fixtures)
	return Fixture{}
}

func TestDisqualifiedEntrantForfeitsEveryMatchItReaches(t *testing.T) {
	l := testLeague(2)
	// Group A is entrants 0, 2, 4, 6: 6 is disqualified during its opening battle.
	battleID := "battle"
	l.Fixtures[0].Status, l.Fixtures[0].BattleID, l.Fixtures[0].Battle = FixtureRunning, &battleID, &BattleOutcome{Status: "running"}
	forfeited := disqualify(t, l, 6)
	opening := l.Fixtures[0]
	side := pick(opening.Home == 6, ForfeitHome, ForfeitAway)
	if len(forfeited) != 1 || forfeited[0].ID != opening.ID || opening.Status != FixtureForfeited || opening.Forfeit != side ||
		opening.BattleID == nil || l.MatchWinner(opening) != 0 || !l.Disqualified(6) || l.Entrants[6].Disqualified.Reason != "too slow" {
		t.Fatalf("forfeited %+v, opening %+v, entrant %+v", forfeited, opening, l.Entrants[6])
	}
	if _, scored := l.Score(opening); scored {
		t.Fatal("a forfeited fixture has a score")
	}
	settle(l, 1, 2, 50, 50)
	round := advance(l)
	// The losers' match opens forfeited: 4 meets 6 and advances without a battle.
	if len(round) != 2 || round[0].Status != FixturePending || round[1].Status != FixtureForfeited || l.MatchWinner(round[1]) != 4 || round[1].BattleID != nil {
		t.Fatalf("second round = %+v", round)
	}
	settle(l, 4, 0, 50, 50)
	decider := advance(l)
	if len(decider) != 1 || pairOf(decider[0]) != [2]int{2, 4} || decider[0].Status != FixturePending {
		t.Fatalf("decider = %+v", decider)
	}
	settle(l, 6, 2, 50, 50)
	requireStandings(t, l.GroupStandings(0), "[{0 0 1 2 0} {2 0 2 2 1} {4 0 3 1 2} {6 0 4 0 2}]")
	if eliminated := l.Eliminated(); !eliminated[6] || !eliminated[4] || eliminated[0] || eliminated[2] {
		t.Fatalf("eliminated = %v", eliminated)
	}
}

func TestCompletedBattleStandsWhenItsEntrantIsDisqualified(t *testing.T) {
	l := testLeague(2)
	// 2 beat 4 in group A's second opening; the scheduler has not recorded it yet.
	battleID := "battle"
	l.Fixtures[1].Status, l.Fixtures[1].BattleID = FixtureRunning, &battleID
	l.Fixtures[1].Battle = &BattleOutcome{Status: BattleCompleted, WinnerFactionID: &l.Entrants[2].FactionID}
	if forfeited := disqualify(t, l, 2); len(forfeited) != 0 || l.Fixtures[1].Status != FixtureRunning {
		t.Fatalf("forfeited %+v, fixture %+v", forfeited, l.Fixtures[1])
	}
}

func TestDisqualifyRefusesWhatItCannotDo(t *testing.T) {
	l := testLeague(2)
	settleBy(l, func(e int) int { return e })
	advance(l)
	settleBy(l, func(e int) int { return e })
	// 6 lost both its group matches.
	cases := map[int]error{-1: ErrEntrantNotFound, 8: ErrEntrantNotFound, 6: ErrEntrantEliminated}
	for entrant, want := range cases {
		if _, err := l.Disqualify(entrant, Disqualification{}); !errors.Is(err, want) {
			t.Errorf("entrant %d: err = %v, want %v", entrant, err, want)
		}
	}
	disqualify(t, l, 0)
	if _, err := l.Disqualify(0, Disqualification{}); !errors.Is(err, ErrAlreadyDisqualified) {
		t.Fatalf("second disqualification err = %v", err)
	}
	// With 0 to 4 disqualified and 6 and 7 out, 5 is the last entrant who can still win.
	for _, entrant := range []int{1, 2, 3, 4} {
		disqualify(t, l, entrant)
	}
	if _, err := l.Disqualify(5, Disqualification{}); !errors.Is(err, ErrLastContender) || l.Disqualified(5) {
		t.Fatalf("last contender err = %v", err)
	}
}

func TestMatchBetweenDisqualifiedEntrantsGoesToTheHigherSeed(t *testing.T) {
	l := testLeague(2)
	byIndex := func(e int) int { return e }
	// Group A is 0, 2, 4, 6. 0 and 2 forfeit their openings and then meet in the losers'
	// match, which the earlier entrant, 0, wins.
	disqualify(t, l, 0)
	disqualify(t, l, 2)
	settleBy(l, byIndex)
	advance(l)
	if f := fixtureOf(t, l, 0, 2); f.Forfeit != ForfeitBoth || l.MatchWinner(f) != 0 {
		t.Fatalf("losers' match = %+v, winner %d", f, l.MatchWinner(f))
	}
	settleBy(l, byIndex)
	advance(l)
	settleBy(l, byIndex)
	requireStandings(t, l.GroupStandings(0), "[{4 0 1 2 0} {6 0 2 2 1} {0 0 3 1 2} {2 0 4 0 2}]")
	// Group A's winner 4 and group B's runner-up 3 are disqualified before their
	// semifinal opens; 4 has the better group finish.
	disqualify(t, l, 4)
	disqualify(t, l, 3)
	semis := advance(l)
	if len(semis) != 2 || pairOf(semis[0]) != [2]int{3, 4} || semis[0].Forfeit != ForfeitBoth || l.MatchWinner(semis[0]) != 4 || semis[1].Status != FixturePending {
		t.Fatalf("semifinals = %+v", semis)
	}
}

func TestForfeitStandsWhenTheWinnerIsDisqualifiedLater(t *testing.T) {
	l := testLeague(2)
	disqualify(t, l, 0)
	disqualify(t, l, 6)
	// 6 won the opening by walkover before it was disqualified, and forfeits its next match.
	if f := l.Fixtures[0]; f.Forfeit == ForfeitBoth || l.MatchWinner(f) != 6 {
		t.Fatalf("opening = %+v, winner %d", f, l.MatchWinner(f))
	}
}

func TestDisqualifiedEntrantNeverBecomesChampion(t *testing.T) {
	l := testLeague(2)
	semis := playGroups(l, func(e int) int { return e })
	// Group A places 0, 2, 4, 6 and group B 1, 3, 5, 7: semifinals 0 v 3 and 1 v 2.
	disqualify(t, l, 0)
	if f := fixtureOf(t, l, 0, 3); f.Status != FixtureForfeited || l.MatchWinner(f) != 3 || len(semis) != 2 {
		t.Fatalf("semifinal = %+v", f)
	}
	settleBy(l, func(e int) int { return e })
	final := advance(l)
	if len(final) != 1 || pairOf(final[0]) != [2]int{1, 3} || final[0].Status != FixturePending {
		t.Fatalf("final = %+v", final)
	}
	// The finalist 1 is disqualified mid-battle, so 3 is champion.
	disqualify(t, l, 1)
	if next, champion := l.Advance(); next != nil || champion == nil || *champion != 3 {
		t.Fatalf("champion = %v, next = %+v", champion, next)
	}
}
