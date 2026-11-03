// Checks the league rules: opening matches, the coin flip for spawn sides, each group's
// dual tournament and placements, the match tiebreak, crossover seeding and progression
// to a champion, and the rules versions its battles ran on.

package league

import (
	"fmt"
	"regexp"
	"slices"
	"testing"
)

func testLeague(groupCount int, scenarios ...string) *League {
	if len(scenarios) == 0 {
		scenarios = []string{"frontier-siege"}
	}
	l := &League{ID: "league", Status: StatusRunning, BattleSalt: "salt", GroupCount: groupCount, Settings: Settings{ScenarioIDs: scenarios, TickCount: 500}}
	for i := range groupCount * GroupSize {
		l.Entrants = append(l.Entrants, Entrant{Index: i, Group: i % groupCount, FactionID: fmt.Sprintf("faction-%02d", i)})
	}
	l.Fixtures = l.OpeningFixtures()
	return l
}

// settle completes fixture i. winner is an entrant index or -1 for a draw; castles are
// the home and away castle health left.
func settle(l *League, i, winner int, homeCastle, awayCastle int64) {
	f := &l.Fixtures[i]
	outcome := &BattleOutcome{Status: BattleCompleted, Factions: map[string]FactionScore{
		l.Entrants[f.Home].FactionID: {CastleHealth: homeCastle, DamageDealt: 100},
		l.Entrants[f.Away].FactionID: {CastleHealth: awayCastle, DamageDealt: 100},
	}}
	if winner >= 0 {
		outcome.WinnerFactionID = &l.Entrants[winner].FactionID
	}
	f.Status, f.Battle = FixtureCompleted, outcome
}

// settleBy completes every unsettled fixture, the lower-ranked entrant per rank winning.
func settleBy(l *League, rank func(int) int) {
	for i, f := range l.Fixtures {
		if f.Settled() {
			continue
		}
		settle(l, i, pick(rank(f.Away) < rank(f.Home), f.Away, f.Home), 50, 50)
	}
}

// advance adds the fixtures Advance opens and returns them.
func advance(l *League) []Fixture {
	next, _ := l.Advance()
	l.Fixtures = append(l.Fixtures, next...)
	return next
}

// playGroups settles every group match by rank, leaving the first knockout stage open.
func playGroups(l *League, rank func(int) int) []Fixture {
	for {
		settleBy(l, rank)
		if next := advance(l); len(next) == 0 || next[0].Stage != StageGroup {
			return next
		}
	}
}

// pairOf is a fixture's sides in entry order, whichever took the first spawn side.
func pairOf(f Fixture) [2]int { return [2]int{min(f.Home, f.Away), max(f.Home, f.Away)} }

func TestOpeningFixturesMatchFirstWithFourthAndSecondWithThird(t *testing.T) {
	for groups, total := range map[int]int{2: 13, 4: 27} {
		l := testLeague(groups, "a", "b", "c")
		if len(l.Fixtures) != 2*groups || TotalFixtures(groups) != total {
			t.Fatalf("%d groups: %d openings, total %d", groups, len(l.Fixtures), TotalFixtures(groups))
		}
		ids := map[string]bool{}
		for i, f := range l.Fixtures {
			group := i / 2
			members := GroupMembers(l.Entrants, group)
			want := [][2]int{{members[0], members[3]}, {members[1], members[2]}}[i%2]
			if f.Sequence != i || f.Status != FixturePending || *f.Group != group || f.Round != 1 || f.Tie != 1+i%2 || pairOf(f) != want || ids[f.ID] ||
				MatchName(f.Stage, f.Round, f.Tie) != MatchOpening || f.ScenarioID != []string{"a", "b", "c"}[(group*MatchesPerGroup+i%2)%3] {
				t.Fatalf("%d groups, fixture %d = %+v", groups, i, f)
			}
			ids[f.ID] = true
		}
	}
	first, again := testLeague(2), testLeague(2)
	for i, f := range first.Fixtures {
		if g := again.Fixtures[i]; g.ID != f.ID || g.Home != f.Home || g.Away != f.Away || g.ScenarioID != f.ScenarioID {
			t.Fatalf("opening fixture %d changed between identical leagues: %+v, %+v", i, f, g)
		}
	}
}

func TestCoinFlipChoosesSidesFromTheSecretSalt(t *testing.T) {
	sides := map[bool]int{}
	for i := range 64 {
		l := testLeague(2)
		l.BattleSalt = fmt.Sprint("salt-", i)
		f := l.OpeningFixtures()[0]
		sides[f.Home == 0]++
		if again := l.OpeningFixtures()[0]; again.Home != f.Home || again.Away != f.Away {
			t.Fatalf("salt %q flipped differently for the same match", l.BattleSalt)
		}
	}
	if sides[true] == 0 || sides[false] == 0 {
		t.Fatalf("the first seed took the first side %d of 64 times", sides[true])
	}
}

func TestEachGroupOpensItsNextRoundOnItsOwn(t *testing.T) {
	l := testLeague(2)
	// Group A is entrants 0, 2, 4, 6: 0 meets 6 and 2 meets 4, the lower index winning.
	settle(l, 0, 0, 50, 50)
	if next, _ := l.Advance(); len(next) != 0 {
		t.Fatalf("opened %+v before group A's openings settled", next)
	}
	settle(l, 1, 2, 50, 50)
	round := advance(l)
	if len(round) != 2 || *round[0].Group != 0 || pairOf(round[0]) != [2]int{0, 2} || pairOf(round[1]) != [2]int{4, 6} ||
		MatchName(round[0].Stage, round[0].Round, round[0].Tie) != MatchWinners || MatchName(round[1].Stage, round[1].Round, round[1].Tie) != MatchLosers ||
		round[0].Sequence != 4 || round[1].Sequence != 5 {
		t.Fatalf("group A round 2 = %+v", round)
	}
	settle(l, 4, 0, 50, 50)
	settle(l, 5, 4, 50, 50)
	decider := advance(l)
	if len(decider) != 1 || pairOf(decider[0]) != [2]int{2, 4} || MatchName(decider[0].Stage, decider[0].Round, decider[0].Tie) != MatchDecider {
		t.Fatalf("group A decider = %+v", decider)
	}
	settle(l, 6, 4, 50, 50)
	requireStandings(t, l.GroupStandings(0), "[{0 0 1 2 0} {4 0 2 2 1} {2 0 3 1 2} {6 0 4 0 2}]")
	requireStandings(t, l.GroupStandings(1), "[{1 1 0 0 0} {3 1 0 0 0} {5 1 0 0 0} {7 1 0 0 0}]")
	if next, _ := l.Advance(); len(next) != 0 {
		t.Fatalf("opened %+v while group B has not played", next)
	}
}

// A pass can create a match's battle and then roll back the match. If another group
// becomes ready before the retry, the match opens at a later sequence, but keeps its ID,
// and so its battle and sides.
func TestFixtureIDsDependOnTheMatchNotThePlayOrder(t *testing.T) {
	l := testLeague(2)
	settle(l, 2, 1, 50, 50)
	settle(l, 3, 3, 50, 50)
	rolledBack, _ := l.Advance()
	settle(l, 0, 0, 50, 50)
	settle(l, 1, 2, 50, 50)
	retried := advance(l)
	if len(rolledBack) != 2 || rolledBack[0].Sequence != 4 || len(retried) != 4 || *retried[0].Group != 0 || retried[2].Sequence != 6 {
		t.Fatalf("rolled back %+v, retried %+v", rolledBack, retried)
	}
	for i, f := range rolledBack {
		if again := retried[2+i]; again.ID != f.ID || again.Home != f.Home || again.Away != f.Away || again.ScenarioID != f.ScenarioID {
			t.Fatalf("group B match %d changed from %+v to %+v", i, f, again)
		}
	}
}

func requireStandings(t *testing.T, got []Standing, want string) {
	t.Helper()
	if fmt.Sprint(got) != want {
		t.Fatalf("standings = %v, want %s", got, want)
	}
}

func TestStandingsPlaceEntrantsAsTheirMatchesSettle(t *testing.T) {
	l := testLeague(2)
	settleBy(l, func(e int) int { return e })
	advance(l)
	// Only group A's winners' match has settled: 0 is first, the others unplaced.
	settle(l, 4, 0, 50, 50)
	requireStandings(t, l.GroupStandings(0), "[{0 0 1 2 0} {2 0 0 1 1} {4 0 0 0 1} {6 0 0 0 1}]")
	settle(l, 5, 4, 50, 50)
	requireStandings(t, l.GroupStandings(0), "[{0 0 1 2 0} {2 0 0 1 1} {4 0 0 1 1} {6 0 4 0 2}]")
	if placements := l.Placements(); placements[0] != 1 || placements[6] != 4 || placements[2] != 0 || placements[1] != 0 {
		t.Fatalf("placements = %v", placements)
	}
}

func TestMatchWinnerBreaksDrawsAndVoidFixtures(t *testing.T) {
	l := testLeague(2)
	f := l.Fixtures[0]
	cases := map[string]struct {
		decide func()
		want   int
	}{
		"battle winner":      {func() { settle(l, 0, 6, 0, 300) }, 6},
		"home castle margin": {func() { settle(l, 0, -1, 30, 0) }, f.Home},
		"away castle margin": {func() { settle(l, 0, -1, 0, 40) }, f.Away},
		"damage margin": {func() {
			settle(l, 0, -1, 0, 0)
			l.Fixtures[0].Battle.Factions[l.Entrants[f.Away].FactionID] = FactionScore{DamageDealt: 500}
		}, f.Away},
		"void falls back to entry order": {func() { l.Fixtures[0].Status, l.Fixtures[0].Battle = FixtureVoid, nil }, 0},
		"cancelled has no winner":        {func() { l.Fixtures[0].Status, l.Fixtures[0].Battle = FixtureCancelled, nil }, -1},
		"pending has no winner":          {func() { l.Fixtures[0].Status, l.Fixtures[0].Battle = FixturePending, nil }, -1},
	}
	for name, test := range cases {
		test.decide()
		if got := l.MatchWinner(l.Fixtures[0]); got != test.want {
			t.Errorf("%s: winner %d, want %d", name, got, test.want)
		}
	}
}

func TestVoidKnockoutMatchGoesToTheBetterGroupFinish(t *testing.T) {
	l := testLeague(2)
	// The higher index is stronger: group A places 6, 4, 2, 0 and group B 7, 5, 3, 1.
	semis := playGroups(l, func(e int) int { return -e })
	if pairOf(semis[0]) != [2]int{5, 6} || pairOf(semis[1]) != [2]int{4, 7} {
		t.Fatalf("semifinals = %+v", semis)
	}
	for i := range semis {
		l.Fixtures[len(l.Fixtures)-2+i].Status = FixtureVoid
	}
	// 6 won group A, so it beats B's runner-up 5 despite entering later.
	ties := l.Ties()
	if *ties[0].Winner != 6 || *ties[1].Winner != 7 {
		t.Fatalf("ties = %+v", ties)
	}
	final := advance(l)
	l.Fixtures[len(l.Fixtures)-1].Status = FixtureVoid
	// Both group winners: the earlier entrant wins.
	if _, champion := l.Advance(); pairOf(final[0]) != [2]int{6, 7} || champion == nil || *champion != 6 {
		t.Fatalf("final %+v, champion %v", final, champion)
	}
}

func TestTwoGroupKnockoutCrossesOverAndCrownsAChampion(t *testing.T) {
	l := testLeague(2)
	semis := playGroups(l, func(e int) int { return e }) // lower entrant index is stronger
	// Group A places 0, 2, 4, 6 and group B 1, 3, 5, 7.
	if len(semis) != 2 || semis[0].Stage != StageSemifinal || pairOf(semis[0]) != [2]int{0, 3} || pairOf(semis[1]) != [2]int{1, 2} ||
		semis[0].Sequence != 10 || semis[1].Tie != 2 || semis[0].Group != nil {
		t.Fatalf("semifinals = %+v", semis)
	}
	if next, _ := l.Advance(); next != nil {
		t.Fatal("advanced before the semifinals settled")
	}
	settleBy(l, func(e int) int { return e })
	final := advance(l)
	if len(final) != 1 || final[0].Stage != StageFinal || pairOf(final[0]) != [2]int{0, 1} {
		t.Fatalf("final = %+v", final)
	}
	settleBy(l, func(e int) int { return -e }) // the underdog wins the final
	if next, champion := l.Advance(); next != nil || champion == nil || *champion != 1 || len(l.Fixtures) != TotalFixtures(2) {
		t.Fatalf("champion = %v, next = %+v, %d fixtures", champion, next, len(l.Fixtures))
	}
	if ties := l.Ties(); len(ties) != 3 || *ties[2].Winner != 1 || ties[0].Stage != StageSemifinal {
		t.Fatalf("ties = %+v", ties)
	}
}

func TestFourGroupBracketKeepsGroupMatesApartUntilTheFinal(t *testing.T) {
	l := testLeague(4, "a", "b")
	quarters := playGroups(l, func(e int) int { return e })
	// Groups A-D are led by 0-3, with runners-up 4-7.
	want := [][2]int{{0, 5}, {1, 4}, {2, 7}, {3, 6}}
	for i, pair := range want {
		if f := quarters[i]; f.Stage != StageQuarterfinal || pairOf(f) != pair || f.Sequence != 20+i {
			t.Fatalf("quarterfinal %d = %+v", i, f)
		}
	}
	settleBy(l, func(e int) int { return e })
	semis := advance(l)
	if len(semis) != 2 || pairOf(semis[0]) != [2]int{0, 2} || pairOf(semis[1]) != [2]int{1, 3} {
		t.Fatalf("semifinals = %+v", semis)
	}
	if semis[0].ScenarioID == semis[1].ScenarioID {
		t.Fatal("knockout maps should keep rotating")
	}
	settleBy(l, func(e int) int { return e })
	advance(l)
	settleBy(l, func(e int) int { return e })
	if _, champion := l.Advance(); champion == nil || *champion != 0 || len(l.Fixtures) != TotalFixtures(4) {
		t.Fatalf("champion %v after %d fixtures", champion, len(l.Fixtures))
	}
}

func TestMatchNames(t *testing.T) {
	got := []string{MatchName(StageGroup, 1, 2), MatchName(StageGroup, 2, 1), MatchName(StageGroup, 2, 2), MatchName(StageGroup, 3, 1), MatchName(StageFinal, 1, 1)}
	if fmt.Sprint(got) != "[opening winners losers decider ]" {
		t.Fatalf("names = %q", got)
	}
}

func TestDerivedIDsAreUUIDs(t *testing.T) {
	pattern := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-5[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	first, second := FixtureBattleID("salt", "fixture", 1), FixtureBattleID("salt", "fixture", 2)
	if !pattern.MatchString(first) || first == second || first != FixtureBattleID("salt", "fixture", 1) {
		t.Fatalf("battle IDs %q %q", first, second)
	}
	if FixtureBattleID("other-salt", "fixture", 1) == first {
		t.Fatal("battle IDs must depend on the league's salt")
	}
}

func TestScoreIgnoresUnfinishedAndUnknownWinners(t *testing.T) {
	l := testLeague(2)
	if _, ok := l.Score(l.Fixtures[0]); ok {
		t.Fatal("pending fixture scored")
	}
	settle(l, 0, -1, 0, 0)
	l.Fixtures[0].Battle.Status = "failed"
	if _, ok := l.Score(l.Fixtures[0]); ok {
		t.Fatal("failed battle scored")
	}
	stranger := "someone-else"
	l.Fixtures[0].Battle.Status, l.Fixtures[0].Battle.WinnerFactionID = BattleCompleted, &stranger
	if score, ok := l.Score(l.Fixtures[0]); !ok || score.Winner != nil {
		t.Fatalf("unknown winner scored as %+v", score)
	}
	if GroupLetter(3) != "D" {
		t.Fatal(GroupLetter(3))
	}
}

func TestRulesVersionsListsEachBattleVersionNewestFirst(t *testing.T) {
	l := testLeague(2)
	if versions := l.RulesVersions(); versions == nil || len(versions) != 0 {
		t.Fatalf("league without battles = %v", versions)
	}
	for i, version := range []string{"12", "13", "12", ""} {
		l.Fixtures[i].Battle = &BattleOutcome{RulesVersion: version, Status: BattleCompleted}
	}
	if versions := l.RulesVersions(); !slices.Equal(versions, []string{"13", "12"}) {
		t.Fatalf("versions = %v", versions)
	}
}
