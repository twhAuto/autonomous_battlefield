// Checks the league rules: the group schedule, standings tiebreakers, crossover
// seeding, two-leg ties and progression from the groups to a champion.

package league

import (
	"fmt"
	"regexp"
	"testing"
)

func testLeague(groupCount int, scenarios ...string) *League {
	if len(scenarios) == 0 {
		scenarios = []string{"frontier-siege"}
	}
	l := &League{ID: "league", Status: StatusRunning, GroupCount: groupCount, Settings: Settings{ScenarioIDs: scenarios, TickCount: 500}}
	for i := range groupCount * GroupSize {
		l.Entrants = append(l.Entrants, Entrant{Index: i, Group: i % groupCount, FactionID: fmt.Sprintf("faction-%02d", i)})
	}
	l.Fixtures = GroupFixtures(l.ID, l.Entrants, groupCount, scenarios)
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
		winner := f.Home
		if rank(f.Away) < rank(f.Home) {
			winner = f.Away
		}
		settle(l, i, winner, 50, 50)
	}
}

func TestGroupFixturesPlayEveryPairingOnceFromEachSide(t *testing.T) {
	for groups, want := range map[int]int{2: 24, 4: 48} {
		l := testLeague(groups, "a", "b", "c")
		if len(l.Fixtures) != want || TotalFixtures(groups) != map[int]int{2: 30, 4: 62}[groups] {
			t.Fatalf("%d groups: %d fixtures, total %d", groups, len(l.Fixtures), TotalFixtures(groups))
		}
		requireFixtureOrder(t, l)
		if pairings := requireBothSidesOnOneMap(t, l); pairings != groups*PairingsPerGroup {
			t.Fatalf("%d pairings, want %d", pairings, groups*PairingsPerGroup)
		}
	}
	if testLeague(2).Fixtures[5].ID != testLeague(2).Fixtures[5].ID {
		t.Fatal("fixture IDs must be stable for retries")
	}
}

// requireFixtureOrder checks sequences, unique IDs, and that both sides share a group.
func requireFixtureOrder(t *testing.T, l *League) {
	t.Helper()
	ids := map[string]bool{}
	for i, f := range l.Fixtures {
		if f.Sequence != i || f.Status != FixturePending || *f.Group != l.Entrants[f.Home].Group || *f.Group != l.Entrants[f.Away].Group || ids[f.ID] {
			t.Fatalf("fixture %d = %+v", i, f)
		}
		ids[f.ID] = true
	}
}

// requireBothSidesOnOneMap checks every pairing is played once from each side on one
// map, and returns the number of pairings.
func requireBothSidesOnOneMap(t *testing.T, l *League) int {
	t.Helper()
	sides, maps := map[[2]int]int{}, map[[2]int]string{}
	for _, f := range l.Fixtures {
		sides[[2]int{f.Home, f.Away}]++
		pair := [2]int{min(f.Home, f.Away), max(f.Home, f.Away)}
		if previous, seen := maps[pair]; seen && previous != f.ScenarioID {
			t.Fatalf("pairing %v changes map between legs", pair)
		}
		maps[pair] = f.ScenarioID
	}
	for side, count := range sides {
		if count != 1 || sides[[2]int{side[1], side[0]}] != 1 {
			t.Fatalf("pairing %v played %d times from this side", side, count)
		}
	}
	return len(maps)
}

func TestDerivedIDsAreUUIDs(t *testing.T) {
	pattern := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-5[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	first, second := FixtureBattleID("fixture", 1), FixtureBattleID("fixture", 2)
	if !pattern.MatchString(first) || first == second || first != FixtureBattleID("fixture", 1) {
		t.Fatalf("battle IDs %q %q", first, second)
	}
}

func TestStandingsRankByPointsThenHeadToHeadThenDifferences(t *testing.T) {
	l := testLeague(2)
	// Group A is entrants 0, 2, 4, 6. Entrant 6 wins everything. 2 and 4 finish level on
	// 7 points; 4 took more points head to head, although 2 has the better castle
	// difference. One 0-4 leg is void.
	for i, f := range l.Fixtures {
		if *f.Group != 0 {
			continue
		}
		pair := [2]int{min(f.Home, f.Away), max(f.Home, f.Away)}
		switch {
		case f.Home == 6 || f.Away == 6:
			settle(l, i, 6, 10, 10)
		case pair == [2]int{2, 4} && f.Leg == 1:
			settle(l, i, 4, 1, 1)
		case pair == [2]int{2, 4}:
			settle(l, i, -1, 0, 0)
		case pair == [2]int{0, 2}:
			settle(l, i, 2, castleFor(f.Home, 2), castleFor(f.Away, 2))
		case f.Leg == 1:
			settle(l, i, 4, 1, 1)
		default:
			l.Fixtures[i].Status = FixtureVoid
		}
	}
	got := l.GroupStandings(0)
	order := []int{got[0].Entrant, got[1].Entrant, got[2].Entrant, got[3].Entrant}
	if fmt.Sprint(order) != "[6 4 2 0]" || got[0].Points != 18 || got[1].Points != 7 || got[2].Points != 7 || got[2].Drawn != 1 || got[1].Played != 5 {
		t.Fatalf("standings = %+v", got)
	}
	if got[2].CastleDifference <= got[1].CastleDifference {
		t.Fatalf("test needs 2 ahead on castle difference: %+v", got)
	}
	empty := testLeague(2).GroupStandings(1)
	if empty[0].Entrant != 1 || empty[3].Entrant != 7 || empty[0].Position != 1 {
		t.Fatalf("unplayed group falls back to entry order: %+v", empty)
	}
}

// castleFor gives the favored entrant a full castle and its opponent none.
func castleFor(side, favored int) int64 {
	if side == favored {
		return 300
	}
	return 0
}

func TestStandingsFallBackToDifferencesAndEntryOrder(t *testing.T) {
	l := testLeague(2)
	for i, f := range l.Fixtures {
		if *f.Group == 1 {
			// Every match is drawn, and entrant 7 always keeps more castle when at home.
			castle := int64(0)
			if f.Home == 7 {
				castle = 20
			}
			settle(l, i, -1, castle, 0)
		}
	}
	got := l.GroupStandings(1)
	if got[0].Entrant != 7 || got[1].Entrant != 1 || got[3].Entrant != 5 || got[0].Points != 6 {
		t.Fatalf("standings = %+v", got)
	}
}

func TestTwoGroupKnockoutCrossesOverAndCrownsAChampion(t *testing.T) {
	l := testLeague(2)
	settleBy(l, func(e int) int { return e }) // lower entrant index is stronger
	next, champion := l.Advance()
	if champion != nil {
		t.Fatalf("champion %v after the groups", champion)
	}
	// Group A order is 0, 2, 4, 6 and group B order 1, 3, 5, 7.
	requireKnockout(t, next, StageSemifinal, 24, [][2]int{{0, 3}, {1, 2}, {3, 0}, {2, 1}})
	l.Fixtures = append(l.Fixtures, next...)
	if next, _ := l.Advance(); next != nil {
		t.Fatal("advanced before the semifinals settled")
	}
	settleBy(l, func(e int) int { return e })
	final, _ := l.Advance()
	if len(final) != 2 || final[0].Stage != StageFinal || final[0].Home != 0 || final[0].Away != 1 {
		t.Fatalf("final = %+v", final)
	}
	l.Fixtures = append(l.Fixtures, final...)
	settleBy(l, func(e int) int { return -e }) // the underdog wins both legs
	if next, champion := l.Advance(); next != nil || champion == nil || *champion != 1 {
		t.Fatalf("champion = %v, next = %+v", champion, next)
	}
	if ties := l.Ties(); len(ties) != 3 || *ties[2].Winner != 1 || ties[0].Stage != StageSemifinal {
		t.Fatalf("ties = %+v", ties)
	}
}

// requireKnockout checks a stage's fixtures: both legs of each tie in order, with sides
// swapped on the second leg.
func requireKnockout(t *testing.T, got []Fixture, stage string, firstSequence int, sides [][2]int) {
	t.Helper()
	if len(got) != len(sides) {
		t.Fatalf("%s: %d fixtures, want %d", stage, len(got), len(sides))
	}
	ties := len(sides) / 2
	for i, f := range got {
		if f.Stage != stage || [2]int{f.Home, f.Away} != sides[i] || f.Sequence != firstSequence+i || f.Leg != 1+i/ties || f.Tie != 1+i%ties {
			t.Fatalf("%s %d = %+v", stage, i, f)
		}
	}
}

func TestFourGroupBracketKeepsGroupMatesApartUntilTheFinal(t *testing.T) {
	l := testLeague(4, "a", "b")
	settleBy(l, func(e int) int { return e })
	quarters, _ := l.Advance()
	// Groups A-D are led by 0-3, with runners-up 4-7.
	want := [][2]int{{0, 5}, {1, 4}, {2, 7}, {3, 6}}
	for i, pair := range want {
		if f := quarters[i]; f.Stage != StageQuarterfinal || [2]int{f.Home, f.Away} != pair {
			t.Fatalf("quarterfinal %d = %+v", i, f)
		}
	}
	l.Fixtures = append(l.Fixtures, quarters...)
	settleBy(l, func(e int) int { return e })
	semis, _ := l.Advance()
	if len(semis) != 4 || semis[0].Home != 0 || semis[0].Away != 2 || semis[1].Home != 1 || semis[1].Away != 3 {
		t.Fatalf("semifinals = %+v", semis)
	}
	if semis[0].ScenarioID == semis[1].ScenarioID {
		t.Fatal("knockout maps should keep rotating")
	}
	l.Fixtures = append(l.Fixtures, semis...)
	settleBy(l, func(e int) int { return e })
	final, _ := l.Advance()
	l.Fixtures = append(l.Fixtures, final...)
	settleBy(l, func(e int) int { return e })
	if _, champion := l.Advance(); champion == nil || *champion != 0 || len(l.Fixtures) != TotalFixtures(4) {
		t.Fatalf("champion %v after %d fixtures", champion, len(l.Fixtures))
	}
}

func TestTieWinnerTiebreaks(t *testing.T) {
	setup := func() *League {
		l := testLeague(2)
		settleBy(l, func(e int) int { return e })
		next, _ := l.Advance()
		l.Fixtures = append(l.Fixtures, next...)
		return l
	}
	// Tie 1 is 0 v 3: leg 1 at index 24 (0 home), leg 2 at index 26 (3 home).
	cases := map[string]struct {
		decide func(*League)
		want   int
	}{
		"more wins":     {func(l *League) { settle(l, 24, 3, 0, 10); settle(l, 26, 3, 10, 0) }, 3},
		"castle margin": {func(l *League) { settle(l, 24, 0, 90, 0); settle(l, 26, 3, 30, 0) }, 0},
		"away castle":   {func(l *League) { settle(l, 24, -1, 0, 0); settle(l, 26, -1, 40, 0) }, 3},
		"void legs fall back to group finish": {func(l *League) {
			l.Fixtures[24].Status, l.Fixtures[26].Status = FixtureVoid, FixtureVoid
		}, 0},
	}
	for name, test := range cases {
		l := setup()
		test.decide(l)
		if got := l.TieWinner(StageSemifinal, 1); got != test.want {
			t.Errorf("%s: winner %d, want %d", name, got, test.want)
		}
	}
	l := setup()
	settle(l, 24, -1, 0, 0)
	settle(l, 26, -1, 0, 0)
	l.Fixtures[26].Battle.Factions[l.Entrants[3].FactionID] = FactionScore{DamageDealt: 500}
	if got := l.TieWinner(StageSemifinal, 1); got != 3 {
		t.Errorf("damage margin: winner %d, want 3", got)
	}
	if got := l.TieWinner(StageFinal, 1); got != -1 {
		t.Errorf("missing tie: %d", got)
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
