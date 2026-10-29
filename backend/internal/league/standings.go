// Decides matches and ranks each group. Every match has a winner: the battle's winner,
// or after a draw or a void fixture the higher castle-health difference, then damage
// difference, then the higher seed. A group places its entrants by the dual tournament:
// the winners'-match winner first, the decider's winner second and loser third, and the
// losers'-match loser fourth.

package league

import "sort"

// Standing is an entrant's group record. Placement is 0 until the group decides it.
type Standing struct {
	Entrant   int
	Group     int
	Placement int
	Won       int
	Lost      int
}

// result is a decided match's winner and loser.
type result struct {
	winner, loser int
}

// MatchWinner returns a completed or void fixture's winner, or -1 while it is undecided
// or after it was cancelled.
func (l *League) MatchWinner(f Fixture) int {
	if f.Status != FixtureCompleted && f.Status != FixtureVoid {
		return -1
	}
	return l.decided(f).winner
}

// decided applies the match tiebreak; the caller checks that the fixture is decided.
func (l *League) decided(f Fixture) result {
	home, away := result{f.Home, f.Away}, result{f.Away, f.Home}
	score, _ := l.Score(f)
	if score.Winner != nil {
		return pick(*score.Winner == f.Home, home, away)
	}
	for _, margin := range []int64{score.CastleDifference, score.DamageDifference} {
		if margin != 0 {
			return pick(margin > 0, home, away)
		}
	}
	return pick(l.higherSeed(f) == f.Home, home, away)
}

// higherSeed is a fixture's better-seeded side: in a group the earlier entrant, in the
// knockout the better group finish, then the earlier entrant.
func (l *League) higherSeed(f Fixture) int {
	first, second := min(f.Home, f.Away), max(f.Home, f.Away)
	if f.Stage == StageGroup {
		return first
	}
	placements := l.Placements()
	return pick(placements[second] < placements[first], second, first)
}

// GroupStandings ranks one group by wins, then fewer losses, then entry order, which
// is also the order of its placements.
func (l *League) GroupStandings(group int) []Standing {
	table := map[int]*Standing{}
	members := GroupMembers(l.Entrants, group)
	for _, member := range members {
		table[member] = &Standing{Entrant: member, Group: group}
	}
	for _, f := range l.groupFixtures(group) {
		if l.MatchWinner(f) < 0 {
			continue
		}
		match := l.decided(f)
		table[match.winner].Won++
		table[match.loser].Lost++
		l.place(table, f, match)
	}
	standings := make([]Standing, 0, len(members))
	for _, member := range members {
		standings = append(standings, *table[member])
	}
	sort.SliceStable(standings, func(i, j int) bool {
		a, b := standings[i], standings[j]
		if a.Won != b.Won {
			return a.Won > b.Won
		}
		return a.Lost < b.Lost
	})
	return standings
}

// place records the placements a decided match settles.
func (l *League) place(table map[int]*Standing, f Fixture, match result) {
	switch MatchName(f.Stage, f.Round, f.Tie) {
	case MatchWinners:
		table[match.winner].Placement = 1
	case MatchDecider:
		table[match.winner].Placement, table[match.loser].Placement = 2, 3
	case MatchLosers:
		table[match.loser].Placement = 4
	}
}

// Placements returns every entrant's group placement, 0 while undecided.
func (l *League) Placements() map[int]int {
	placements := map[int]int{}
	for group := range l.GroupCount {
		for _, standing := range l.GroupStandings(group) {
			placements[standing.Entrant] = standing.Placement
		}
	}
	return placements
}
